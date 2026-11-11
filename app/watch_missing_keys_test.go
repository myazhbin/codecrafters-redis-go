package main

import "testing"

func TestExecAbortsWhenWatchedMissingKeyWasCreated(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")

	do(t, other, "SET", "foo", "200")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "300")
	expectReply(t, conn, queued)

	// Going from missing to existing is a modification.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\n200\r\n")
}

func TestExecAbortsWhenWatchedMissingKeyWasCreatedWithAnyType(t *testing.T) {
	creations := map[string][]string{
		"string":  {"SET", "foo", "1"},
		"counter": {"INCR", "foo"},
		"list":    {"RPUSH", "foo", "a"},
		"stream":  {"XADD", "foo", "1-1", "k", "v"},
	}

	for name, command := range creations {
		t.Run(name, func(t *testing.T) {
			addr := startTestServer(t)
			conn := dial(t, addr)
			other := dial(t, addr)

			do(t, conn, "WATCH", "foo")
			expectReply(t, conn, "+OK\r\n")
			do(t, other, command...)
			drainPendingReply(t, other)

			do(t, conn, "MULTI")
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "SET", "bar", "500")
			expectReply(t, conn, queued)
			do(t, conn, "EXEC")
			expectReply(t, conn, "*-1\r\n")
			do(t, conn, "GET", "bar")
			expectReply(t, conn, "$-1\r\n")
		})
	}
}

func TestExecRunsWhenWatchedMissingKeyIsStillMissing(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")

	// Looking for the key, or failing to remove something from it, doesn't
	// create it.
	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")
	do(t, other, "LPOP", "foo")
	expectReply(t, other, "$-1\r\n")
	do(t, other, "SET", "other", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "300")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\n300\r\n")
}

func TestExecAbortsWhenWatchedMissingKeyWasCreatedThenRemoved(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")

	// The key is missing again at EXEC time, but it was touched in between.
	do(t, other, "RPUSH", "foo", "a")
	expectReply(t, other, ":1\r\n")
	do(t, other, "LPOP", "foo")
	expectReply(t, other, "$1\r\na\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "1")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecAbortsWhenWatchedKeyWasRemoved(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, other, "RPUSH", "foo", "a")
	expectReply(t, other, ":1\r\n")
	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")

	// The opposite transition, from existing to missing, counts as well.
	do(t, other, "LPOP", "foo")
	expectReply(t, other, "$1\r\na\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "1")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecAbortsWhenOneWatchedKeyExistsAndTheMissingOneIsCreated(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "existing", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "existing", "missing")
	expectReply(t, conn, "+OK\r\n")

	do(t, other, "SET", "missing", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "existing")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "existing")
	expectReply(t, conn, "$1\r\n1\r\n")
}
