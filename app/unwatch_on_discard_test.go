package main

import "testing"

func TestDiscardClearsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "200")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "300")
	expectReply(t, conn, queued)

	do(t, other, "SET", "foo", "400")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// The modification made before DISCARD doesn't abort the next
	// transaction.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "300")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	do(t, other, "GET", "bar")
	expectReply(t, other, "$3\r\n300\r\n")
}

func TestKeysAreNotWatchedAnymoreAfterDiscard(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// Modifications made after DISCARD don't count either.
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, queued)
	do(t, other, "SET", "foo", "2")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestWatchAfterDiscardProtectsTheNextTransaction(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	watchAndQueue(t, conn, "foo", "SET", "bar", "2")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$-1\r\n")
}

func TestDiscardWithoutMultiKeepsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	// A DISCARD that is rejected discards nothing.
	do(t, conn, "DISCARD")
	expectReply(t, conn, "-ERR DISCARD without MULTI\r\n")

	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestDiscardOnlyClearsTheWatchOfItsOwnConnection(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)
	writer := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "a", "1")
	watchAndQueue(t, other, "foo", "SET", "b", "1")

	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")
	do(t, writer, "SET", "foo", "1")
	expectReply(t, writer, "+OK\r\n")

	do(t, other, "EXEC")
	expectReply(t, other, "*-1\r\n")
}

func TestDiscardUnregistersTheClient(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.watchers) != 0 {
		t.Errorf("%d keys are still watched after DISCARD, want none", len(srv.watchers))
	}
}
