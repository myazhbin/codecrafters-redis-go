package main

import (
	"io"
	"net"
	"testing"
	"time"
)

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

// expectReply reads exactly len(want) bytes and compares them to want.
func expectReply(t *testing.T, conn net.Conn, want string) {
	t.Helper()

	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading reply: %v (got %q so far, want %q)", err, got, want)
	}
	if string(got) != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func TestRespondsToPing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	expectReply(t, conn, "+PONG\r\n")
}
