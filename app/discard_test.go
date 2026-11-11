package main

import "testing"

func TestDiscardAbortsTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// None of the queued commands was executed.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")

	// The transaction is over, so there is nothing left to discard.
	do(t, conn, "DISCARD")
	expectReply(t, conn, "-ERR DISCARD without MULTI\r\n")
}

func TestDiscardWithoutMulti(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "DISCARD")
	expectReply(t, conn, "-ERR DISCARD without MULTI\r\n")

	// The connection still works normally.
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestDiscardIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "multi")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "discard")
	expectReply(t, conn, "+OK\r\n")
}

func TestDiscardEmptyTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")
}

func TestDiscardKeepsExistingValues(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, queued)
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n1\r\n")
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+none\r\n")
}

func TestCommandsRunNormallyAfterDiscard(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// Commands are executed right away again, instead of being queued.
	do(t, conn, "SET", "foo", "5")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":6\r\n")

	// And EXEC has no transaction to execute.
	do(t, conn, "EXEC")
	expectReply(t, conn, "-ERR EXEC without MULTI\r\n")
}

func TestNewTransactionAfterDiscardStartsEmpty(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// The discarded commands don't leak into the next transaction.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "bar")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n"))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestDiscardOnlyAffectsItsOwnConnection(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, queued)

	do(t, other, "MULTI")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "bar", "2")
	expectReply(t, other, queued)

	do(t, conn, "DISCARD")
	expectReply(t, conn, "+OK\r\n")

	// The other client's transaction is untouched.
	do(t, other, "EXEC")
	expectReply(t, other, array("+OK\r\n"))
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$1\r\n2\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")

	// A client that isn't in a transaction can't discard someone else's.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "DISCARD")
	expectReply(t, other, "-ERR DISCARD without MULTI\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
}

func TestDiscardWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "DISCARD", "extra")
	expectReply(t, conn, "-ERR wrong number of arguments for 'discard' command\r\n")
}
