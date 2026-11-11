package main

import (
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// startTestServer runs a new Server on a random local port for the duration
// of the test and returns the address to dial.
func startTestServer(t *testing.T) string {
	t.Helper()
	return startServer(t, NewServer())
}

// startServer runs srv on a random local port for the duration of the test and
// returns the address to dial.
func startServer(t *testing.T, srv *Server) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()

	t.Cleanup(func() {
		l.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned an error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Serve did not return after the listener was closed")
		}
	})

	return l.Addr().String()
}

// fakeClock is a manually advanced clock, used to test expiry without
// sleeping. It's safe for concurrent use.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2024, time.March, 10, 12, 0, 0, 0, time.UTC)}
}

// Now returns the clock's current time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// startTestServerWithClock is like startTestServer, but the server's notion of
// time is driven by the returned clock.
func startTestServerWithClock(t *testing.T) (string, *fakeClock) {
	t.Helper()

	clock := newFakeClock()
	srv := NewServer()
	srv.store.now = clock.Now
	return startServer(t, srv), clock
}

// dial connects to the test server and fails the test if it can't.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// encodeCommand encodes a command the way Redis clients send it: as a RESP
// array of bulk strings.
func encodeCommand(args ...string) string {
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, arg := range args {
		b.WriteString("$" + strconv.Itoa(len(arg)) + "\r\n" + arg + "\r\n")
	}
	return b.String()
}

// send writes raw bytes to the server.
func send(t *testing.T, conn net.Conn, data string) {
	t.Helper()

	if _, err := conn.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// do sends a command to the server.
func do(t *testing.T, conn net.Conn, args ...string) {
	t.Helper()
	send(t, conn, encodeCommand(args...))
}

// replyTimeout is how long tests wait for a reply before giving up.
const replyTimeout = 2 * time.Second

// expectReply reads exactly len(want) bytes and compares them to want, the
// raw RESP encoding of the expected reply.
func expectReply(t *testing.T, conn net.Conn, want string) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	got := make([]byte, len(want))
	if n, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading reply: %v (got %q so far, want %q)", err, got[:n], want)
	}
	if string(got) != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

// expectNoReply checks that the server sends nothing for the duration d,
// which is how a blocked client looks like from the outside.
func expectNoReply(t *testing.T, conn net.Conn, d time.Duration) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if n > 0 {
		t.Fatalf("unexpected reply while the client should be blocked: %q", buf[:n])
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("waiting for nothing to arrive: unexpected error: %v", err)
	}
}

// eventually polls condition until it holds, and fails the test if it still
// doesn't after a couple of seconds.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(replyTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// expectClosed checks that the server closed the connection without sending
// anything more.
func expectClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	rest, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("waiting for the server to close the connection: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("unexpected extra data before close: %q", rest)
	}
}

// bulkArray returns the RESP encoding of an array of bulk strings.
func bulkArray(items ...string) string {
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(items)) + "\r\n")
	for _, item := range items {
		b.WriteString("$" + strconv.Itoa(len(item)) + "\r\n" + item + "\r\n")
	}
	return b.String()
}

// readReplyLine reads from conn up to and including the next CRLF, and
// returns the line without it.
func readReplyLine(t *testing.T, conn net.Conn) string {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	var line []byte
	buf := make([]byte, 1)
	for !strings.HasSuffix(string(line), "\r\n") {
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatalf("reading a line: %v (got %q so far)", err, line)
		}
		line = append(line, buf[0])
	}
	return string(line[:len(line)-2])
}

// readBulkString reads a bulk string reply and returns its content.
func readBulkString(t *testing.T, conn net.Conn) string {
	t.Helper()

	header := readReplyLine(t, conn)
	if !strings.HasPrefix(header, "$") {
		t.Fatalf("reply starts with %q, want a bulk string", header)
	}
	length, err := strconv.Atoi(header[1:])
	if err != nil || length < 0 {
		t.Fatalf("invalid bulk string header %q", header)
	}

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	payload := make([]byte, length+2)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatalf("reading a bulk string of %d bytes: %v", length, err)
	}
	if string(payload[length:]) != "\r\n" {
		t.Fatalf("bulk string is not terminated by CRLF: %q", payload)
	}
	return string(payload[:length])
}

// array returns the RESP encoding of an array whose elements are the given,
// already encoded, values.
func array(items ...string) string {
	return "*" + strconv.Itoa(len(items)) + "\r\n" + strings.Join(items, "")
}

// entryReply returns the RESP encoding of a stream entry: an array holding
// its ID and the array of its fields and values.
func entryReply(id string, fields ...string) string {
	return array(bulk(id), bulkArray(fields...))
}
