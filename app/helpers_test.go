package main

import (
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
	conn.SetDeadline(time.Now().Add(2 * time.Second))
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

// expectReply reads exactly len(want) bytes and compares them to want, the
// raw RESP encoding of the expected reply.
func expectReply(t *testing.T, conn net.Conn, want string) {
	t.Helper()

	got := make([]byte, len(want))
	if n, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading reply: %v (got %q so far, want %q)", err, got[:n], want)
	}
	if string(got) != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

// expectClosed checks that the server closed the connection without sending
// anything more.
func expectClosed(t *testing.T, conn net.Conn) {
	t.Helper()

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
