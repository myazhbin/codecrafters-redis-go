package main

import (
	"net"
	"testing"
	"time"
)

// watchAndQueue makes conn watch key, start a transaction and queue command.
func watchAndQueue(t *testing.T, conn net.Conn, key string, command ...string) {
	t.Helper()

	do(t, conn, "WATCH", key)
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, command...)
	expectReply(t, conn, queued)
}

func TestExecAbortsWhenWatchedKeyWasModified(t *testing.T) {
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

	// The transaction is aborted: the reply is a null array and the queued
	// command never ran.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$3\r\n200\r\n")
}

func TestExecRunsWhenAnotherKeyWasModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "baz", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "caz", "200")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "baz", "SET", "caz", "400")

	// Only the watched key matters.
	do(t, other, "SET", "caz", "300")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
	do(t, other, "GET", "caz")
	expectReply(t, other, "$3\r\n400\r\n")
}

func TestExecRunsWhenWatchedKeyWasNotModified(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "INCR", "foo")

	// Reading the watched key isn't modifying it.
	do(t, other, "GET", "foo")
	expectReply(t, other, "$1\r\n1\r\n")
	do(t, other, "TYPE", "foo")
	expectReply(t, other, "+string\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":2\r\n"))
}

func TestExecAbortsEvenIfTheValueIsBackToWhatItWas(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "SET", "bar", "1")

	// The key was touched, even though its value is the same in the end.
	do(t, other, "SET", "foo", "999")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "foo", "100")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecAbortsWhenTheSameValueIsWrittenAgain(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "SET", "bar", "1")

	do(t, other, "SET", "foo", "100")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecAbortsWhenModifiedBeforeMulti(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	// The watch starts at WATCH, not at MULTI.
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "1")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$-1\r\n")
}

func TestExecIgnoresModificationsMadeBeforeWatch(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "INCR", "foo")
	expectReply(t, other, ":2\r\n")

	watchAndQueue(t, conn, "foo", "INCR", "foo")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":3\r\n"))
}

func TestExecAbortsWhenTheWatcherItselfModifiedTheKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	// It doesn't matter who touched the key.
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n1\r\n")
}

func TestExecWritingTheWatchedKeyInTheTransactionIsFine(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "counter", "10")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "counter")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "counter", "11")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, queued)

	// Queued commands only run at EXEC: they can't abort their own
	// transaction.
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":12\r\n"))
}

func TestExecAbortsAfterEveryKindOfWrite(t *testing.T) {
	writes := []struct {
		name  string
		setup [][]string
		write []string
	}{
		{name: "SET", write: []string{"SET", "key", "v"}},
		{name: "SET with expiry", write: []string{"SET", "key", "v", "PX", "100000"}},
		{name: "INCR", write: []string{"INCR", "key"}},
		{name: "RPUSH new list", write: []string{"RPUSH", "key", "a"}},
		{name: "RPUSH existing list", setup: [][]string{{"RPUSH", "key", "a"}}, write: []string{"RPUSH", "key", "b"}},
		{name: "LPUSH existing list", setup: [][]string{{"RPUSH", "key", "a"}}, write: []string{"LPUSH", "key", "b"}},
		{name: "LPOP", setup: [][]string{{"RPUSH", "key", "a", "b"}}, write: []string{"LPOP", "key"}},
		{name: "LPOP last element", setup: [][]string{{"RPUSH", "key", "a"}}, write: []string{"LPOP", "key"}},
		{name: "LPOP count", setup: [][]string{{"RPUSH", "key", "a", "b", "c"}}, write: []string{"LPOP", "key", "2"}},
		{name: "LPOP whole list", setup: [][]string{{"RPUSH", "key", "a", "b"}}, write: []string{"LPOP", "key", "5"}},
		{name: "BLPOP", setup: [][]string{{"RPUSH", "key", "a", "b"}}, write: []string{"BLPOP", "key", "1"}},
		{name: "XADD new stream", write: []string{"XADD", "key", "1-1", "k", "v"}},
		{name: "XADD existing stream", setup: [][]string{{"XADD", "key", "1-1", "k", "v"}}, write: []string{"XADD", "key", "1-2", "k", "v"}},
	}

	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			addr := startTestServer(t)
			conn := dial(t, addr)
			other := dial(t, addr)

			for _, command := range tc.setup {
				do(t, other, command...)
			}
			drainPendingReply(t, other)

			watchAndQueue(t, conn, "key", "SET", "result", "ran")
			do(t, other, tc.write...)
			drainPendingReply(t, other)

			do(t, conn, "EXEC")
			expectReply(t, conn, "*-1\r\n")
			do(t, conn, "GET", "result")
			expectReply(t, conn, "$-1\r\n")
		})
	}
}

func TestExecRunsAfterCommandsThatDoNotModify(t *testing.T) {
	commands := [][]string{
		{"GET", "list_key"},  // wrong type
		{"INCR", "list_key"}, // wrong type
		{"LRANGE", "list_key", "0", "-1"},
		{"LLEN", "list_key"},
		{"LPOP", "list_key", "0"},
		{"RPUSH", "string_key", "a"},            // wrong type
		{"INCR", "text_key"},                    // not an integer
		{"XADD", "stream_key", "1-1", "k", "v"}, // ID too small
		{"XRANGE", "stream_key", "-", "+"},
		{"XREAD", "STREAMS", "stream_key", "0-0"},
		{"TYPE", "string_key"},
		{"LPOP", "missing_key"},
	}

	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	for _, command := range [][]string{
		{"RPUSH", "list_key", "a"},
		{"SET", "string_key", "1"},
		{"SET", "text_key", "abc"},
		{"XADD", "stream_key", "5-5", "k", "v"},
	} {
		do(t, other, command...)
	}
	drainPendingReply(t, other)

	do(t, conn, "WATCH", "list_key", "string_key", "text_key", "stream_key", "missing_key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "result", "ran")
	expectReply(t, conn, queued)

	for _, command := range commands {
		do(t, other, command...)
	}
	drainPendingReply(t, other)

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecAbortsWhenWatchedKeyWasModifiedByATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")

	do(t, other, "MULTI")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, queued)

	// A command that is only queued hasn't modified anything yet...
	do(t, other, "EXEC")
	expectReply(t, other, array("+OK\r\n"))

	// ...but it has once the other transaction is executed.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecRunsWhenTheOtherTransactionWasDiscarded(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")

	do(t, other, "MULTI")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, queued)
	do(t, other, "DISCARD")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecAbortEndsTheTransaction(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, other, "SET", "foo", "1")
	expectReply(t, other, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")

	// The connection is out of the transaction: commands run right away.
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "-ERR EXEC without MULTI\r\n")
}

func TestExecAbortOnlyConcernsTheWatchingClient(t *testing.T) {
	addr := startTestServer(t)
	watcher := dial(t, addr)
	bystander := dial(t, addr)
	writer := dial(t, addr)

	watchAndQueue(t, watcher, "foo", "SET", "a", "1")
	do(t, bystander, "MULTI")
	expectReply(t, bystander, "+OK\r\n")
	do(t, bystander, "SET", "b", "1")
	expectReply(t, bystander, queued)

	do(t, writer, "SET", "foo", "1")
	expectReply(t, writer, "+OK\r\n")

	do(t, bystander, "EXEC")
	expectReply(t, bystander, array("+OK\r\n"))
	do(t, watcher, "EXEC")
	expectReply(t, watcher, "*-1\r\n")
}

func TestExecAbortsEveryClientWatchingTheKey(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)
	writer := dial(t, addr)

	watchAndQueue(t, first, "foo", "SET", "a", "1")
	watchAndQueue(t, second, "foo", "SET", "b", "1")

	do(t, writer, "SET", "foo", "1")
	expectReply(t, writer, "+OK\r\n")

	do(t, first, "EXEC")
	expectReply(t, first, "*-1\r\n")
	do(t, second, "EXEC")
	expectReply(t, second, "*-1\r\n")
}

func TestExecOfOneWatcherAbortsTheOther(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "SET", "counter", "10")
	expectReply(t, first, "+OK\r\n")

	// The classic use of optimistic locking: two clients race to update the
	// same key, and only one of them wins.
	watchAndQueue(t, first, "counter", "SET", "counter", "11")
	watchAndQueue(t, second, "counter", "SET", "counter", "12")

	do(t, first, "EXEC")
	expectReply(t, first, array("+OK\r\n"))
	do(t, second, "EXEC")
	expectReply(t, second, "*-1\r\n")

	do(t, first, "GET", "counter")
	expectReply(t, first, "$2\r\n11\r\n")
}

func TestExecAbortsWhenWatchedKeyExpired(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "SET", "bar", "1")

	// Nobody touches the key: it goes away on its own.
	clock.Advance(200 * time.Millisecond)

	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}

func TestExecRunsWhenWatchedKeyHasNotExpiredYet(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	clock.Advance(50 * time.Millisecond)

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestExecRunsWhenWatchedKeyWasAlreadyExpiredAtWatch(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	clock.Advance(200 * time.Millisecond)

	// The key no longer exists when it starts being watched, even though it
	// hasn't been removed from memory yet.
	watchAndQueue(t, conn, "foo", "SET", "bar", "1")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n"))
}

func TestWatchersAreForgottenWhenTheClientDisconnects(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)

	do(t, conn, "WATCH", "a", "b")
	expectReply(t, conn, "+OK\r\n")

	watched := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.watchers)
	}
	if got := watched(); got != 2 {
		t.Fatalf("%d keys are watched, want 2", got)
	}

	conn.Close()
	eventually(t, "the watches of the disconnected client to be dropped", func() bool {
		return watched() == 0
	})
}
