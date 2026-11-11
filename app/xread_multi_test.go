package main

import "testing"

func TestXReadMultipleStreams(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "temperature", "95")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "other_stream_key", "0-2", "humidity", "97")
	expectReply(t, conn, "$3\r\n0-2\r\n")

	// All the keys come first, then one ID per key, in the same order.
	do(t, conn, "XREAD", "streams", "stream_key", "other_stream_key", "0-0", "0-1")
	expectReply(t, conn, array(
		streamReply("stream_key", entryReply("0-1", "temperature", "95")),
		streamReply("other_stream_key", entryReply("0-2", "humidity", "97")),
	))
}

func TestXReadMultipleStreamsExactEncoding(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "a", "1-0", "f", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "b", "2-0", "g", "2")
	expectReply(t, conn, "$3\r\n2-0\r\n")

	do(t, conn, "XREAD", "STREAMS", "a", "b", "0-0", "0-0")
	expectReply(t, conn, ""+
		"*2\r\n"+
		"*2\r\n$1\r\na\r\n"+
		"*1\r\n*2\r\n$3\r\n1-0\r\n*2\r\n$1\r\nf\r\n$1\r\n1\r\n"+
		"*2\r\n$1\r\nb\r\n"+
		"*1\r\n*2\r\n$3\r\n2-0\r\n*2\r\n$1\r\ng\r\n$1\r\n2\r\n")
}

func TestXReadMultipleStreamsFollowsCommandOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Created in the order b, c, a.
	for _, key := range []string{"b", "c", "a"} {
		do(t, conn, "XADD", key, "1-1", "key", key)
		expectReply(t, conn, "$3\r\n1-1\r\n")
	}

	// The reply follows the order of the command: neither the order the
	// streams were created in, nor the alphabetical one.
	do(t, conn, "XREAD", "STREAMS", "c", "a", "b", "0-0", "0-0", "0-0")
	expectReply(t, conn, array(
		streamReply("c", entryReply("1-1", "key", "c")),
		streamReply("a", entryReply("1-1", "key", "a")),
		streamReply("b", entryReply("1-1", "key", "b")),
	))
}

func TestXReadMultipleStreamsEachHasItsOwnID(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, id := range []string{"1-0", "2-0", "3-0"} {
		do(t, conn, "XADD", "first", id, "s", "first")
		expectReply(t, conn, bulk(id))
		do(t, conn, "XADD", "second", id, "s", "second")
		expectReply(t, conn, bulk(id))
	}

	do(t, conn, "XREAD", "STREAMS", "first", "second", "2-0", "0-0")
	expectReply(t, conn, array(
		streamReply("first", entryReply("3-0", "s", "first")),
		streamReply("second",
			entryReply("1-0", "s", "second"),
			entryReply("2-0", "s", "second"),
			entryReply("3-0", "s", "second"),
		),
	))
}

func TestXReadMultipleStreamsOmitsStreamsWithNothingNew(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "first", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "second", "1-0", "b", "2")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "third", "1-0", "c", "3")
	expectReply(t, conn, "$3\r\n1-0\r\n")

	// Only the streams that have entries after their ID are in the reply.
	do(t, conn, "XREAD", "STREAMS", "first", "second", "third", "1-0", "0-0", "5-0")
	expectReply(t, conn, array(streamReply("second", entryReply("1-0", "b", "2"))))

	do(t, conn, "XREAD", "STREAMS", "first", "second", "third", "0-0", "1-0", "0-0")
	expectReply(t, conn, array(
		streamReply("first", entryReply("1-0", "a", "1")),
		streamReply("third", entryReply("1-0", "c", "3")),
	))

	// When none of them has, the reply is a null array.
	do(t, conn, "XREAD", "STREAMS", "first", "second", "third", "1-0", "1-0", "1-0")
	expectReply(t, conn, nullArrayReply)
}

func TestXReadMultipleStreamsWithMissingStreams(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "existing", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")

	// A stream that doesn't exist is simply a stream with no entries.
	do(t, conn, "XREAD", "STREAMS", "missing", "existing", "also-missing", "0-0", "0-0", "0-0")
	expectReply(t, conn, array(streamReply("existing", entryReply("1-0", "a", "1"))))

	do(t, conn, "XREAD", "STREAMS", "missing", "also-missing", "0-0", "0-0")
	expectReply(t, conn, nullArrayReply)

	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestXReadSameStreamTwice(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "stream_key", "2-0", "b", "2")
	expectReply(t, conn, "$3\r\n2-0\r\n")

	// Each occurrence is read with its own ID.
	do(t, conn, "XREAD", "STREAMS", "stream_key", "stream_key", "0-0", "1-0")
	expectReply(t, conn, array(
		streamReply("stream_key", entryReply("1-0", "a", "1"), entryReply("2-0", "b", "2")),
		streamReply("stream_key", entryReply("2-0", "b", "2")),
	))
}

func TestXReadManyStreams(t *testing.T) {
	conn := dial(t, startTestServer(t))

	keys := []string{"s1", "s2", "s3", "s4", "s5", "s6"}
	var want []string
	command := []string{"XREAD", "STREAMS"}
	for _, key := range keys {
		do(t, conn, "XADD", key, "7-7", "key", key)
		expectReply(t, conn, "$3\r\n7-7\r\n")
		want = append(want, streamReply(key, entryReply("7-7", "key", key)))
		command = append(command, key)
	}
	for range keys {
		command = append(command, "0-0")
	}

	do(t, conn, command...)
	expectReply(t, conn, array(want...))
}

func TestXReadMultipleStreamsUnbalanced(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "first", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")

	// Three keys but two IDs, and the other way around.
	do(t, conn, "XREAD", "STREAMS", "first", "second", "third", "0-0", "0-0")
	expectReply(t, conn, unbalancedXReadErr)
	do(t, conn, "XREAD", "STREAMS", "first", "0-0", "0-0")
	expectReply(t, conn, unbalancedXReadErr)
}

func TestXReadMultipleStreamsValidatesEveryArgument(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")

	// One bad key or ID fails the whole command, wherever it is.
	do(t, conn, "XREAD", "STREAMS", "stream_key", "text", "0-0", "0-0")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "XREAD", "STREAMS", "text", "stream_key", "0-0", "0-0")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "XREAD", "STREAMS", "stream_key", "other", "0-0", "oops")
	expectReply(t, conn, invalidStreamIDError)
	do(t, conn, "XREAD", "STREAMS", "stream_key", "other", "oops", "0-0")
	expectReply(t, conn, invalidStreamIDError)
}
