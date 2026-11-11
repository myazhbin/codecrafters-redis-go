package main

import (
	"net"
	"testing"
	"time"
)

func TestXReadBlockZeroWaitsIndefinitely(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "stream_key", "0-1", "temperature", "96")
	expectReply(t, writer, "$3\r\n0-1\r\n")

	// A timeout of 0 is not "don't wait": it's "wait for as long as it takes".
	do(t, reader, "XREAD", "BLOCK", "0", "streams", "stream_key", "0-1")
	expectNoReply(t, reader, 500*time.Millisecond)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "0-2", "temperature", "95")
	expectReply(t, writer, "$3\r\n0-2\r\n")
	expectReply(t, reader, array(
		streamReply("stream_key", entryReply("0-2", "temperature", "95")),
	))
	waitForBlockedReaders(t, srv, "stream_key", 0)
}

func TestXReadBlockZeroReturnsAvailableEntriesImmediately(t *testing.T) {
	t.Parallel()
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, array(streamReply("stream_key", entryReply("0-1", "foo", "bar"))))
}

func TestXReadBlockZeroOnMissingStream(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "0-0")
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))))
}

func TestXReadBlockZeroKeepsWaitingAfterIrrelevantEntries(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "10-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	// Entries at or below the requested ID don't end the wait.
	for _, id := range []string{"1-0", "5-0", "10-0"} {
		do(t, writer, "XADD", "stream_key", id, "id", id)
		expectReply(t, writer, bulk(id))
	}
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "10-1", "id", "10-1")
	expectReply(t, writer, "$4\r\n10-1\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("10-1", "id", "10-1"))))
}

func TestXReadBlockZeroOnSeveralStreams(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "first", "second", "third", "0-0", "0-0", "0-0")
	waitForBlockedReaders(t, srv, "third", 1)

	do(t, writer, "XADD", "third", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")
	expectReply(t, reader, array(streamReply("third", entryReply("1-1", "foo", "bar"))))

	for _, key := range []string{"first", "second", "third"} {
		waitForBlockedReaders(t, srv, key, 0)
	}
}

func TestXReadBlockZeroManyReaders(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	writer := dial(t, addr)

	const readers = 5
	conns := make([]net.Conn, readers)
	for i := range conns {
		conns[i] = dial(t, addr)
		do(t, conns[i], "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "0-0")
		waitForBlockedReaders(t, srv, "stream_key", i+1)
	}

	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")

	want := array(streamReply("stream_key", entryReply("1-1", "foo", "bar")))
	for _, conn := range conns {
		expectReply(t, conn, want)
	}
}

func TestXReadBlockZeroDisconnectedClientIsForgotten(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "0", "STREAMS", "first", "second", "0-0", "0-0")
	waitForBlockedReaders(t, srv, "first", 1)
	waitForBlockedReaders(t, srv, "second", 1)

	// Without timeout, disconnecting is the only other way out of the wait.
	reader.Close()
	waitForBlockedReaders(t, srv, "first", 0)
	waitForBlockedReaders(t, srv, "second", 0)
}

func TestXReadBlockZeroMixedWithTimedReaders(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	forever := dial(t, addr)
	timed := dial(t, addr)
	writer := dial(t, addr)

	do(t, forever, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "0-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)
	do(t, timed, "XREAD", "BLOCK", "100", "STREAMS", "stream_key", "0-0")

	// The timed reader gives up; the other one is unaffected.
	expectReply(t, timed, nullArrayReply)
	expectNoReply(t, forever, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")
	expectReply(t, forever, array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))))
}
