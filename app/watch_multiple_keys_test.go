package main

import "testing"

func TestExecAbortsWhenOneOfSeveralWatchedKeysWasModified(t *testing.T) {
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

	do(t, other, "SET", "foo", "200")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, other, "GET", "bar")
	expectReply(t, other, "$3\r\n200\r\n")
}

func TestExecAbortsWhicheverOfTheWatchedKeysWasModified(t *testing.T) {
	keys := []string{"foo", "bar", "baz"}

	for _, modified := range keys {
		t.Run(modified, func(t *testing.T) {
			addr := startTestServer(t)
			conn := dial(t, addr)
			other := dial(t, addr)

			do(t, conn, append([]string{"WATCH"}, keys...)...)
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "MULTI")
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "SET", "result", "ran")
			expectReply(t, conn, queued)

			do(t, other, "SET", modified, "1")
			expectReply(t, other, "+OK\r\n")

			do(t, conn, "EXEC")
			expectReply(t, conn, "*-1\r\n")
			do(t, conn, "GET", "result")
			expectReply(t, conn, "$-1\r\n")
		})
	}
}

func TestExecRunsWhenNoneOfSeveralWatchedKeysWasModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo", "bar", "baz")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "400")
	expectReply(t, conn, queued)

	do(t, other, "SET", "qux", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecAbortsWhenAllWatchedKeysWereModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "bar", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestKeysWatchedBySeveralWatchCommandsAddUp(t *testing.T) {
	for _, modified := range []string{"foo", "bar", "baz"} {
		t.Run(modified, func(t *testing.T) {
			addr := startTestServer(t)
			conn := dial(t, addr)
			other := dial(t, addr)

			// A new WATCH adds to the keys already watched; it doesn't
			// replace them.
			do(t, conn, "WATCH", "foo")
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "WATCH", "bar", "baz")
			expectReply(t, conn, "+OK\r\n")

			do(t, other, "SET", modified, "1")
			expectReply(t, other, "+OK\r\n")

			do(t, conn, "MULTI")
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "EXEC")
			expectReply(t, conn, "*-1\r\n")
		})
	}
}

func TestWatchingAKeyAgainKeepsEarlierModifications(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	// Watching the key again, alone or with others, doesn't start over.
	do(t, conn, "WATCH", "foo", "bar", "foo")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecAbortsWhenWatchedKeysOfDifferentTypesAreModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, other, "RPUSH", "list_key", "a")
	expectReply(t, other, ":1\r\n")
	do(t, other, "SET", "string_key", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "WATCH", "string_key", "list_key", "stream_key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "string_key")
	expectReply(t, conn, queued)

	do(t, other, "RPUSH", "list_key", "b")
	expectReply(t, other, ":2\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "string_key")
	expectReply(t, conn, "$1\r\n1\r\n")
}
