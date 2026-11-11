package main

import (
	"testing"
	"time"
)

const queued = "+QUEUED\r\n"

func TestCommandsAreQueuedAfterMulti(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	// Queued commands are not executed: seen from another connection, the
	// key doesn't exist.
	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")
}

func TestQueuedCommandsDoNotAlterExistingData(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "counter", "10")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"SET", "counter", "99"},
		{"INCR", "counter"},
		{"RPUSH", "list_key", "c"},
		{"LPUSH", "list_key", "z"},
		{"LPOP", "list_key"},
		{"LPOP", "list_key", "2"},
		{"XADD", "stream_key", "2-0", "baz", "qux"},
		{"SET", "new_key", "value", "PX", "100"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}

	// Everything is as it was before MULTI.
	do(t, other, "GET", "counter")
	expectReply(t, other, "$2\r\n10\r\n")
	do(t, other, "LRANGE", "list_key", "0", "-1")
	expectReply(t, other, bulkArray("a", "b"))
	do(t, other, "XRANGE", "stream_key", "-", "+")
	expectReply(t, other, array(entryReply("1-1", "foo", "bar")))
	do(t, other, "TYPE", "new_key")
	expectReply(t, other, "+none\r\n")
}

func TestReadCommandsAreQueuedToo(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// Inside a transaction even commands that only read reply QUEUED: their
	// result is only known once the transaction is executed.
	for _, command := range [][]string{
		{"GET", "foo"},
		{"PING"},
		{"PING", "hello"},
		{"ECHO", "hey"},
		{"TYPE", "foo"},
		{"LLEN", "list_key"},
		{"LRANGE", "list_key", "0", "-1"},
		{"XRANGE", "stream_key", "-", "+"},
		{"XREAD", "STREAMS", "stream_key", "0-0"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}
}

func TestBlockingCommandsAreQueuedWithoutBlocking(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// These would block outside of a transaction; here they're just queued.
	start := time.Now()
	do(t, conn, "BLPOP", "list_key", "0")
	expectReply(t, conn, queued)
	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	expectReply(t, conn, queued)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("queueing blocking commands took %v", elapsed)
	}
}

func TestQueueingIsCaseInsensitive(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "multi")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "set", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "InCr", "foo")
	expectReply(t, conn, queued)

	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")
}

func TestOnlyTheConnectionInATransactionQueues(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "from-transaction")
	expectReply(t, conn, queued)

	// The other client's commands are executed right away, as usual.
	do(t, other, "SET", "foo", "from-other")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "GET", "foo")
	expectReply(t, other, "$10\r\nfrom-other\r\n")
	do(t, other, "INCR", "counter")
	expectReply(t, other, ":1\r\n")
}

func TestInvalidCommandsAreNotQueued(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// Commands that can't possibly run are rejected right away.
	do(t, conn, "NOSUCHCOMMAND", "arg")
	expectReply(t, conn, "-ERR unknown command 'NOSUCHCOMMAND', with args beginning with: 'arg' \r\n")
	do(t, conn, "GET")
	expectReply(t, conn, "-ERR wrong number of arguments for 'get' command\r\n")
	do(t, conn, "SET", "foo")
	expectReply(t, conn, "-ERR wrong number of arguments for 'set' command\r\n")

	// The transaction is still open, and valid commands are still queued.
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, queued)
	do(t, conn, "MULTI")
	expectReply(t, conn, "-ERR MULTI calls can not be nested\r\n")
}

func TestCommandsWithBadArgumentsAreStillQueued(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "xyz")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// Whether these fail can only be known when they run, so they're queued.
	do(t, conn, "INCR", "text")
	expectReply(t, conn, queued)
	do(t, conn, "LRANGE", "text", "zero", "one")
	expectReply(t, conn, queued)
	do(t, conn, "RPUSH", "text", "element")
	expectReply(t, conn, queued)
	do(t, conn, "SET", "foo", "bar", "PX", "soon")
	expectReply(t, conn, queued)
}

func TestCommandsAreNotQueuedAfterTheTransactionEnded(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "queued")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	readReplyLine(t, conn) // the array header; its content is tested elsewhere
	drainPendingReply(t, conn)

	// Back to normal: commands are executed and reply with their result.
	do(t, conn, "SET", "foo", "direct")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$6\r\ndirect\r\n")
}

func TestQueuedCommandsAreDroppedOnDisconnect(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, queued)
	conn.Close()

	// A transaction that is never executed never happens.
	time.Sleep(50 * time.Millisecond)
	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")
	do(t, other, "LLEN", "list_key")
	expectReply(t, other, ":0\r\n")
}

func TestManyCommandsCanBeQueued(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for i := 0; i < 500; i++ {
		do(t, conn, "INCR", "counter")
		expectReply(t, conn, queued)
	}

	do(t, other, "GET", "counter")
	expectReply(t, other, "$-1\r\n")
}

func TestQueuedCommandsPipelined(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	send(t, conn, encodeCommand("MULTI")+encodeCommand("SET", "foo", "41")+encodeCommand("INCR", "foo"))
	expectReply(t, conn, "+OK\r\n"+queued+queued)

	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")
}
