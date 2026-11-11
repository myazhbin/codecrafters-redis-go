package main

import "testing"

func TestUnwatch(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")
}

func TestUnwatchWithoutWatchedKeys(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "unwatch")
	expectReply(t, conn, "+OK\r\n")
}

func TestUnwatchWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "UNWATCH", "foo")
	expectReply(t, conn, "-ERR wrong number of arguments for 'unwatch' command\r\n")
}

func TestExecRunsAfterUnwatchEvenIfWatchedKeyWasModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "200")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")

	do(t, other, "SET", "foo", "200")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "400")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))

	do(t, other, "GET", "foo")
	expectReply(t, other, "$3\r\n400\r\n")
}

func TestExecRunsWhenKeyIsModifiedAfterUnwatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "1")
	expectReply(t, conn, queued)

	// The key isn't watched anymore, wherever the modification happens.
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestUnwatchClearsEveryWatchedKey(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "a")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "b", "c")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")

	for _, key := range []string{"a", "b", "c"} {
		do(t, other, "SET", key, "1")
		expectReply(t, other, "+OK\r\n")
	}

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
}

func TestWatchAgainAfterUnwatchStartsOver(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")

	// The modification seen by the previous watch is forgotten...
	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	// ...but a new one counts.
	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "SET", "foo", "2")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestUnwatchOnlyConcernsItsOwnConnection(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)
	writer := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "WATCH", "foo")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")

	do(t, writer, "SET", "foo", "1")
	expectReply(t, writer, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	// The other client still watches the key.
	do(t, other, "MULTI")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "EXEC")
	expectReply(t, other, "*-1\r\n")
}

func TestUnwatchInsideMultiIsQueued(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	// Like any other command, UNWATCH only runs at EXEC: too late to save a
	// transaction whose watched key was already modified.
	do(t, conn, "UNWATCH")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestUnwatchUnregistersTheClient(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "WATCH", "a", "b")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "UNWATCH")
	expectReply(t, conn, "+OK\r\n")

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.watchers) != 0 {
		t.Errorf("%d keys are still watched after UNWATCH, want none", len(srv.watchers))
	}
}
