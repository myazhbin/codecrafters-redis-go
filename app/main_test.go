package main

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const pingCommand = "*1\r\n$4\r\nPING\r\n"

// startTestServer serves a single connection with handleConnection on a random
// local port and returns the address to dial.
func startTestServer(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleConnection(conn)
	}()

	return l.Addr().String()
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

// send writes raw bytes to the server.
func send(t *testing.T, conn net.Conn, data string) {
	t.Helper()

	if _, err := conn.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// expectReply reads exactly len(want) bytes and compares them to want.
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

func TestRespondsToPing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	send(t, conn, pingCommand)
	expectReply(t, conn, "+PONG\r\n")
}

func TestRespondsToMultiplePings(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Each PING is sent only after the previous reply arrived, like redis-cli does.
	for i := 0; i < 5; i++ {
		send(t, conn, pingCommand)
		expectReply(t, conn, "+PONG\r\n")
	}
}

func TestRespondsToPipelinedPings(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// All commands arrive in a single write; each still gets its own reply.
	send(t, conn, strings.Repeat(pingCommand, 3))
	expectReply(t, conn, strings.Repeat("+PONG\r\n", 3))
}

func TestRespondsToCommandSplitAcrossWrites(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A command may arrive in several TCP segments: no reply until it's complete.
	send(t, conn, "*1\r\n$4\r\nPI")
	time.Sleep(50 * time.Millisecond)
	send(t, conn, "NG\r\n")
	expectReply(t, conn, "+PONG\r\n")
}

func TestClosesConnectionWhenClientDisconnects(t *testing.T) {
	conn := dial(t, startTestServer(t))

	send(t, conn, pingCommand)
	expectReply(t, conn, "+PONG\r\n")

	// Half-close: the server sees EOF, stops its loop and closes its side.
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	expectClosed(t, conn)
}

func TestProtocolErrorClosesConnection(t *testing.T) {
	conn := dial(t, startTestServer(t))

	send(t, conn, "*1\r\n+PING\r\n")
	expectReply(t, conn, "-ERR Protocol error: expected '$', got '+'\r\n")
	expectClosed(t, conn)
}
