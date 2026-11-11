package main

import "testing"

func TestWatch(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")
}

func TestWatchIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "watch", "key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WaTcH", "key")
	expectReply(t, conn, "+OK\r\n")
}

func TestWatchSeveralKeys(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "key1", "key2", "key3")
	expectReply(t, conn, "+OK\r\n")
}

func TestWatchExistingAndMissingKeys(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")

	// Any key can be watched, whatever it holds and whether it exists.
	do(t, conn, "WATCH", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "list_key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "missing")
	expectReply(t, conn, "+OK\r\n")

	// Watching a key doesn't create it or change it.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n1\r\n")
	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestWatchTheSameKeyTwice(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WATCH", "key", "key")
	expectReply(t, conn, "+OK\r\n")
}

func TestWatchWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH")
	expectReply(t, conn, "-ERR wrong number of arguments for 'watch' command\r\n")
}

func TestWatchLeavesTheConnectionUsable(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WATCH", "key")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "key")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}
