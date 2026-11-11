package main

import "testing"

func TestMulti(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
}

func TestMultiIsCaseInsensitive(t *testing.T) {
	for _, name := range []string{"multi", "Multi", "mULTI"} {
		conn := dial(t, startTestServer(t))

		do(t, conn, name)
		expectReply(t, conn, "+OK\r\n")
	}
}

func TestMultiCannotBeNested(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "MULTI")
	expectReply(t, conn, "-ERR MULTI calls can not be nested\r\n")
	do(t, conn, "multi")
	expectReply(t, conn, "-ERR MULTI calls can not be nested\r\n")
}

func TestMultiIsPerConnection(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	// Each connection has its own transaction state: the second MULTI isn't
	// nested in the first one.
	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")
}

func TestMultiOnNewConnectionAfterDisconnect(t *testing.T) {
	addr := startTestServer(t)

	first := dial(t, addr)
	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	first.Close()

	// The transaction died with its connection.
	second := dial(t, addr)
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")
}

func TestMultiWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI", "now")
	expectReply(t, conn, "-ERR wrong number of arguments for 'multi' command\r\n")

	// The rejected command did not start a transaction.
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
}

func TestMultiDoesNotAffectOtherClients(t *testing.T) {
	addr := startTestServer(t)
	inTransaction := dial(t, addr)
	other := dial(t, addr)

	do(t, inTransaction, "MULTI")
	expectReply(t, inTransaction, "+OK\r\n")

	do(t, other, "SET", "foo", "bar")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "GET", "foo")
	expectReply(t, other, "$3\r\nbar\r\n")
	do(t, other, "PING")
	expectReply(t, other, "+PONG\r\n")
}
