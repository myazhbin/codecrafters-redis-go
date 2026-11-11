package main

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	pingCommand = "*1\r\n$4\r\nPING\r\n"
	pong        = "+PONG\r\n"
)

func TestRespondsToMultipleCommandsOnOneConnection(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Each PING is sent only after the previous reply arrived, like redis-cli does.
	for i := 0; i < 5; i++ {
		send(t, conn, pingCommand)
		expectReply(t, conn, pong)
	}
}

func TestRespondsToPipelinedCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// All commands arrive in a single write; each still gets its own reply, in order.
	send(t, conn, pingCommand+encodeCommand("ECHO", "one")+pingCommand+encodeCommand("ECHO", "two"))
	expectReply(t, conn, pong+"$3\r\none\r\n"+pong+"$3\r\ntwo\r\n")
}

func TestRespondsToCommandSplitAcrossWrites(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A command may arrive in several TCP segments: no reply until it's complete.
	send(t, conn, "*2\r\n$4\r\nEC")
	time.Sleep(50 * time.Millisecond)
	send(t, conn, "HO\r\n$5\r\nhe")
	time.Sleep(50 * time.Millisecond)
	send(t, conn, "llo\r\n")
	expectReply(t, conn, "$5\r\nhello\r\n")
}

func TestClosesConnectionWhenClientDisconnects(t *testing.T) {
	conn := dial(t, startTestServer(t))

	send(t, conn, pingCommand)
	expectReply(t, conn, pong)

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

func TestHandlesConcurrentClients(t *testing.T) {
	addr := startTestServer(t)

	// Both clients are connected before either of them sends anything.
	first := dial(t, addr)
	second := dial(t, addr)

	// The client that connected last is served even though the first one is
	// still connected and idle.
	send(t, second, pingCommand)
	expectReply(t, second, pong)

	send(t, first, pingCommand)
	expectReply(t, first, pong)

	// Interleaved commands on both connections.
	for i := 0; i < 3; i++ {
		send(t, first, pingCommand)
		send(t, second, pingCommand)
		expectReply(t, second, pong)
		expectReply(t, first, pong)
	}
}

func TestHandlesManyClientsInParallel(t *testing.T) {
	addr := startTestServer(t)

	const clients, commandsPerClient = 25, 10

	// Connect everyone up front so all connections are open at the same time.
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(id int, conn net.Conn) {
			defer wg.Done()
			for n := 0; n < commandsPerClient; n++ {
				// Every client echoes its own messages, so a reply delivered
				// to the wrong connection is detected.
				message := fmt.Sprintf("client-%d-message-%d", id, n)
				want := fmt.Sprintf("$%d\r\n%s\r\n", len(message), message)

				if _, err := conn.Write([]byte(encodeCommand("ECHO", message))); err != nil {
					errs <- fmt.Errorf("client %d: write: %w", id, err)
					return
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(conn, got); err != nil {
					errs <- fmt.Errorf("client %d: read: %w", id, err)
					return
				}
				if string(got) != want {
					errs <- fmt.Errorf("client %d: reply = %q, want %q", id, got, want)
					return
				}
			}
		}(i, conn)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestClientDisconnectDoesNotAffectOthers(t *testing.T) {
	addr := startTestServer(t)

	leaving := dial(t, addr)
	staying := dial(t, addr)

	send(t, leaving, pingCommand)
	expectReply(t, leaving, pong)
	leaving.Close()

	send(t, staying, pingCommand)
	expectReply(t, staying, pong)

	// New clients are still accepted afterwards.
	late := dial(t, addr)
	send(t, late, strings.Repeat(pingCommand, 2))
	expectReply(t, late, strings.Repeat(pong, 2))
}
