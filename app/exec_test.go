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

func TestExecEmptyTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")

	// No command was queued: the transaction succeeds with no replies. This
	// is an empty array, not a null one.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	// EXEC ended the transaction, so a second one has nothing to execute.
	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)
}

func TestExecEndsTheTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	// A new transaction can be started: it isn't nested in the previous one.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
}

func TestExecEmptyTransactionsBackToBack(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for i := 0; i < 5; i++ {
		do(t, conn, "MULTI")
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, "EXEC")
		expectReply(t, conn, "*0\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)
}

func TestExecEmptyTransactionPipelined(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// All three commands arrive in a single write.
	send(t, conn, encodeCommand("MULTI")+encodeCommand("EXEC")+encodeCommand("EXEC"))
	expectReply(t, conn, "+OK\r\n"+"*0\r\n"+execWithoutMultiError)
}

func TestExecEmptyTransactionIsPerConnection(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")

	// Ending one transaction leaves the other one open.
	do(t, first, "EXEC")
	expectReply(t, first, "*0\r\n")
	do(t, first, "EXEC")
	expectReply(t, first, execWithoutMultiError)

	do(t, second, "EXEC")
	expectReply(t, second, "*0\r\n")
}

func TestExecAfterFailedNestedMulti(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "-ERR MULTI calls can not be nested\r\n")

	// The rejected MULTI didn't open a second level: one EXEC ends it all.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)
}

func TestExecLeavesTheConnectionUsable(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")

	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
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
