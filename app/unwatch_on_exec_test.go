package main

import "testing"

func TestAbortedExecClearsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "200")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "SET", "bar", "300")

	do(t, other, "SET", "foo", "200")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")

	// The watch protected that transaction only: the next one isn't aborted
	// because of it.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "300")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	do(t, other, "GET", "bar")
	expectReply(t, other, "$3\r\n300\r\n")
}

func TestSuccessfulExecClearsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	// foo isn't watched anymore, so modifying it now has no consequence.
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecClearsTheWatchEvenIfTheKeyKeepsBeingModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, queued)
	// Modified once more, in the middle of the second transaction.
	do(t, other, "SET", "foo", "2")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecDoesNotLetItsOwnWritesAbortTheNextTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The transaction writes the very key it watches.
	watchAndQueue(t, conn, "foo", "SET", "foo", "1")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":2\r\n"))
}

func TestWatchAfterExecProtectsTheNextTransaction(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	// Watching again after EXEC works like the first time.
	watchAndQueue(t, conn, "foo", "SET", "bar", "2")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$1\r\n1\r\n")
}

func TestEmptyExecClearsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
}

func TestExecWithoutMultiKeepsTheWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	// An EXEC that is rejected executes nothing, and ends nothing.
	do(t, conn, "EXEC")
	expectReply(t, conn, "-ERR EXEC without MULTI\r\n")

	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecUnregistersTheClient(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.watchers) != 0 {
		t.Errorf("%d keys are still watched after EXEC, want none", len(srv.watchers))
	}
}
