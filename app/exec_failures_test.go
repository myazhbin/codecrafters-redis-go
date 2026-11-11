package main

import "testing"

const notAnIntegerReply = "-ERR value is not an integer or out of range\r\n"
const wrongTypeReply = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"

func TestExecReportsFailureAndRunsTheOtherCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "abc")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "41")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "bar")
	expectReply(t, conn, queued)

	// The error takes the place of the failed command's reply; the command
	// after it still runs.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*2\r\n-ERR value is not an integer or out of range\r\n:42\r\n")

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nabc\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$2\r\n42\r\n")
}

func TestExecFailureInTheMiddle(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "xyz")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "SET", "bar", "7")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", notAnIntegerReply, "+OK\r\n"))

	// Nothing is rolled back: the commands before the failure keep their
	// effects too.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nxyz\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$1\r\n7\r\n")
}

func TestExecFailureAsLastCommand(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, queued)
	do(t, conn, "RPUSH", "foo", "a")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", wrongTypeReply))
}

func TestExecEveryCommandFails(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "abc")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "LLEN", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, array(notAnIntegerReply, wrongTypeReply, notAnIntegerReply))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nabc\r\n")
}

func TestExecFailuresOfDifferentCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "5-5", "k", "v")
	expectReply(t, conn, "$3\r\n5-5\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	commands := [][]string{
		{"GET", "list_key"},
		{"INCR", "list_key"},
		{"XADD", "stream_key", "1-1", "k", "v"},
		{"XADD", "list_key", "*", "k", "v"},
		{"SET", "ok", "1"},
		{"LPOP", "list_key", "nope"},
		{"INCR", "ok"},
	}
	for _, command := range commands {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}

	do(t, conn, "EXEC")
	expectReply(t, conn, array(
		wrongTypeReply,
		wrongTypeReply,
		"-ERR The ID specified in XADD is equal or smaller than the target stream top item\r\n",
		wrongTypeReply,
		"+OK\r\n",
		"-ERR value is out of range, must be positive\r\n",
		":2\r\n",
	))
}

func TestExecFailureEndsTheTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "abc")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(notAnIntegerReply))

	// The connection is back to normal: commands run right away, and there is
	// no transaction left to execute or to discard.
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "-ERR EXEC without MULTI\r\n")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "-ERR DISCARD without MULTI\r\n")
}

func TestExecFailureIsOnlyReportedToItsClient(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "abc")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "SET", "bar", "7")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(notAnIntegerReply, "+OK\r\n"))

	// The other client only sees the effects of the commands that succeeded.
	do(t, other, "GET", "bar")
	expectReply(t, other, "$1\r\n7\r\n")
	do(t, other, "GET", "foo")
	expectReply(t, other, "$3\r\nabc\r\n")
}
