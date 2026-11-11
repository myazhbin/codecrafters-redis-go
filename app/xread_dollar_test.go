package main

import (
	"testing"
	"time"
)

func TestXReadBlockWithDollarReturnsOnlyNewEntries(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "stream_key", "0-1", "temperature", "96")
	expectReply(t, writer, "$3\r\n0-1\r\n")

	// "$" stands for the last entry at the time of the command: the entry
	// that is already there isn't returned, so the client waits.
	do(t, reader, "XREAD", "BLOCK", "0", "streams", "stream_key", "$")
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "0-2", "temperature", "95")
	expectReply(t, writer, "$3\r\n0-2\r\n")
	expectReply(t, reader, array(
		streamReply("stream_key", entryReply("0-2", "temperature", "95")),
	))
}

func TestXReadBlockWithDollarTimesOut(t *testing.T) {
	t.Parallel()
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "temperature", "96")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-2", "temperature", "95")
	expectReply(t, conn, "$3\r\n0-2\r\n")

	start := time.Now()
	do(t, conn, "XREAD", "BLOCK", "150", "streams", "stream_key", "$")
	expectNoReply(t, conn, 75*time.Millisecond)
	expectReply(t, conn, nullArrayReply)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("XREAD timed out after %v, before its 150ms timeout", elapsed)
	}
}

func TestXReadBlockWithDollarOnMissingStream(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	// The stream doesn't exist yet: whatever gets added first is new.
	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n0-1\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("0-1", "foo", "bar"))))
}

func TestXReadWithDollarWithoutBlockReturnsNothing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	// By definition nothing comes after the last entry.
	do(t, conn, "XREAD", "STREAMS", "stream_key", "$")
	expectReply(t, conn, nullArrayReply)
	do(t, conn, "XREAD", "STREAMS", "missing", "$")
	expectReply(t, conn, nullArrayReply)
}

func TestXReadBlockWithDollarIsResolvedWhenTheCommandIsReceived(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	for _, id := range []string{"1-0", "2-0", "3-0"} {
		do(t, writer, "XADD", "stream_key", id, "id", id)
		expectReply(t, writer, bulk(id))
	}

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	// None of the three earlier entries is delivered, only the new one.
	do(t, writer, "XADD", "stream_key", "4-0", "id", "4-0")
	expectReply(t, writer, "$3\r\n4-0\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("4-0", "id", "4-0"))))

	// Each command resolves "$" again: the entry just received is now the
	// last one, so the client waits for a fifth entry.
	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)
	do(t, writer, "XADD", "stream_key", "5-0", "id", "5-0")
	expectReply(t, writer, "$3\r\n5-0\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("5-0", "id", "5-0"))))
}

func TestXReadBlockWithDollarAndGeneratedIDs(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "stream_key", "*", "n", "1")
	readBulkString(t, writer)

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "*", "n", "2")
	id := readBulkString(t, writer)
	expectReply(t, reader, array(streamReply("stream_key", entryReply(id, "n", "2"))))
}

func TestXReadBlockWithDollarOnSeveralStreams(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "first", "1-0", "a", "1")
	expectReply(t, writer, "$3\r\n1-0\r\n")
	do(t, writer, "XADD", "second", "1-0", "b", "2")
	expectReply(t, writer, "$3\r\n1-0\r\n")

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "first", "second", "$", "$")
	waitForBlockedReaders(t, srv, "first", 1)
	waitForBlockedReaders(t, srv, "second", 1)

	do(t, writer, "XADD", "second", "2-0", "c", "3")
	expectReply(t, writer, "$3\r\n2-0\r\n")
	expectReply(t, reader, array(streamReply("second", entryReply("2-0", "c", "3"))))
}

func TestXReadWithDollarMixedWithExplicitIDs(t *testing.T) {
	t.Parallel()
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "first", "1-0", "a", "1")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "second", "1-0", "b", "2")
	expectReply(t, conn, "$3\r\n1-0\r\n")

	// "$" applies to its own stream only: the other one is read from 0-0 and
	// has an entry to return, so the command doesn't block.
	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "first", "second", "$", "0-0")
	expectReply(t, conn, array(streamReply("second", entryReply("1-0", "b", "2"))))
}

func TestXReadBlockWithDollarTwoReaders(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	first := dial(t, addr)
	second := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "stream_key", "1-0", "old", "entry")
	expectReply(t, writer, "$3\r\n1-0\r\n")

	do(t, first, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 1)
	do(t, second, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 2)

	do(t, writer, "XADD", "stream_key", "2-0", "new", "entry")
	expectReply(t, writer, "$3\r\n2-0\r\n")

	want := array(streamReply("stream_key", entryReply("2-0", "new", "entry")))
	expectReply(t, first, want)
	expectReply(t, second, want)
}

func TestXReadDollarIsOnlyAnIDForXRead(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "$", "foo", "bar")
	expectReply(t, conn, invalidStreamIDError)
	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XRANGE", "stream_key", "$", "+")
	expectReply(t, conn, invalidStreamIDError)

	// "$" is still type-checked against the key.
	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "text", "$")
	expectReply(t, conn, wrongTypeError)
}
