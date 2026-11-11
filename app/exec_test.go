package main

import "testing"

const execWithoutMultiError = "-ERR EXEC without MULTI\r\n"

func TestExecWithoutMulti(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)
}

func TestExecWithoutMultiRepeatedly(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The error doesn't put the connection in any special state.
	for i := 0; i < 3; i++ {
		do(t, conn, "EXEC")
		expectReply(t, conn, execWithoutMultiError)
	}
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestExecWithoutMultiAfterOtherCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":42\r\n")

	// Commands that ran outside of a transaction aren't a transaction.
	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$2\r\n42\r\n")
}

func TestExecNeedsMultiOnTheSameConnection(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")

	// The transaction belongs to the first connection only.
	do(t, second, "EXEC")
	expectReply(t, second, execWithoutMultiError)
}

func TestExecIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"exec", "Exec", "eXEC"} {
		do(t, conn, name)
		expectReply(t, conn, execWithoutMultiError)
	}
}

func TestExecWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "EXEC", "now")
	expectReply(t, conn, "-ERR wrong number of arguments for 'exec' command\r\n")
}
