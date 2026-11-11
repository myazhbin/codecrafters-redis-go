package main

import (
	"net"
	"testing"
)

const watchInsideMultiReply = "-ERR WATCH inside MULTI is not allowed\r\n"

func TestWatchInsideMultiIsRejected(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, watchInsideMultiReply)
}

func TestWatchBeforeMultiIsAccepted(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "counter")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "another_key")
	expectReply(t, conn, watchInsideMultiReply)
}

func TestWatchInsideMultiIsNotQueued(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, queued)
	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, watchInsideMultiReply)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	// The rejected WATCH is neither part of the transaction nor the end of
	// it: the commands queued around it run as usual.
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":2\r\n"))
}

func TestWatchInsideMultiSeveralKeys(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "a", "b", "c")
	expectReply(t, conn, watchInsideMultiReply)
	do(t, conn, "watch", "a")
	expectReply(t, conn, watchInsideMultiReply)
}

func TestWatchIsAllowedAgainOnceTheTransactionIsOver(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, watchInsideMultiReply)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")
}

func TestWatchInsideMultiIsPerConnection(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// Another client's transaction doesn't prevent this one from watching.
	do(t, other, "WATCH", "key")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, watchInsideMultiReply)
}

func TestWatchRemembersTheKeysOfTheConnection(t *testing.T) {
	srv := NewServer()
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	c := &client{server: srv, conn: serverSide}
	other := &client{server: srv, conn: serverSide}

	if got := string(c.execute([]string{"WATCH", "a", "b"})); got != "+OK\r\n" {
		t.Fatalf("WATCH a b = %q, want +OK", got)
	}
	if got := string(c.execute([]string{"WATCH", "b", "c"})); got != "+OK\r\n" {
		t.Fatalf("WATCH b c = %q, want +OK", got)
	}

	if len(c.watched) != 3 {
		t.Fatalf("client watches %d keys, want 3: %v", len(c.watched), c.watched)
	}
	for _, key := range []string{"a", "b", "c"} {
		if _, ok := c.watched[key]; !ok {
			t.Errorf("key %q is not watched", key)
		}
	}
	if len(other.watched) != 0 {
		t.Errorf("another client watches %v, want nothing", other.watched)
	}

	// A WATCH rejected inside a transaction watches nothing.
	c.execute([]string{"MULTI"})
	c.execute([]string{"WATCH", "d"})
	if _, ok := c.watched["d"]; ok {
		t.Errorf("key watched inside MULTI was recorded")
	}
}
