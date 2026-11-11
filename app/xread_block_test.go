package main

import (
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"
)

// waitForBlockedReaders waits until exactly n clients are blocked reading the
// stream at key.
func waitForBlockedReaders(t *testing.T, srv *Server, key string, n int) {
	t.Helper()

	eventually(t, fmt.Sprintf("%d client(s) to be blocked reading %q", n, key), func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.streamWaiters[key]) == n
	})
}

func TestXReadBlockReturnsEntryAddedWhileWaiting(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "stream_key", "0-1", "temperature", "96")
	expectReply(t, writer, "$3\r\n0-1\r\n")

	// Nothing comes after 0-1 yet: the client waits.
	do(t, reader, "XREAD", "BLOCK", "1000", "streams", "stream_key", "0-1")
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "0-2", "temperature", "95")
	expectReply(t, writer, "$3\r\n0-2\r\n")

	// The new entry is delivered right away, well before the timeout.
	start := time.Now()
	expectReply(t, reader, array(
		streamReply("stream_key", entryReply("0-2", "temperature", "95")),
	))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("the blocked client got the entry after %v, it should be immediate", elapsed)
	}
	waitForBlockedReaders(t, srv, "stream_key", 0)
}

func TestXReadBlockTimesOut(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)

	do(t, conn, "XADD", "stream_key", "0-2", "temperature", "95")
	expectReply(t, conn, "$3\r\n0-2\r\n")

	start := time.Now()
	do(t, conn, "XREAD", "block", "150", "streams", "stream_key", "0-2")

	// Nothing comes back while the timeout is running, then a null array.
	expectNoReply(t, conn, 75*time.Millisecond)
	expectReply(t, conn, nullArrayReply)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("XREAD timed out after %v, before its 150ms timeout", elapsed)
	}

	// The client isn't waiting anymore, and can go on.
	waitForBlockedReaders(t, srv, "stream_key", 0)
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestXReadBlockReturnsAvailableEntriesImmediately(t *testing.T) {
	t.Parallel()
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "a", "1")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-2", "b", "2")
	expectReply(t, conn, "$3\r\n0-2\r\n")

	start := time.Now()
	do(t, conn, "XREAD", "BLOCK", "5000", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, array(streamReply("stream_key",
		entryReply("0-1", "a", "1"),
		entryReply("0-2", "b", "2"),
	)))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("XREAD took %v although entries were available", elapsed)
	}
}

func TestXReadBlockOnStreamThatDoesNotExistYet(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "2000", "STREAMS", "stream_key", "0-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	// The entry that creates the stream unblocks the reader.
	do(t, writer, "XADD", "stream_key", "5-0", "foo", "bar")
	expectReply(t, writer, "$3\r\n5-0\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("5-0", "foo", "bar"))))
}

func TestXReadBlockDeliversToEveryWaitingClient(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	writer := dial(t, addr)

	const readers = 4
	conns := make([]net.Conn, readers)
	for i := range conns {
		conns[i] = dial(t, addr)
		do(t, conns[i], "XREAD", "BLOCK", "2000", "STREAMS", "stream_key", "0-0")
		waitForBlockedReaders(t, srv, "stream_key", i+1)
	}

	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")

	// Reading a stream doesn't consume its entries: all the readers get it.
	want := array(streamReply("stream_key", entryReply("1-1", "foo", "bar")))
	for _, conn := range conns {
		expectReply(t, conn, want)
	}
	waitForBlockedReaders(t, srv, "stream_key", 0)
}

func TestXReadBlockOnSeveralStreams(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, writer, "XADD", "first", "1-0", "a", "1")
	expectReply(t, writer, "$3\r\n1-0\r\n")

	do(t, reader, "XREAD", "BLOCK", "2000", "STREAMS", "first", "second", "1-0", "0-0")
	waitForBlockedReaders(t, srv, "first", 1)
	waitForBlockedReaders(t, srv, "second", 1)

	// An entry in any of the streams unblocks the client; the reply only
	// holds the streams that have something new.
	do(t, writer, "XADD", "second", "7-0", "b", "2")
	expectReply(t, writer, "$3\r\n7-0\r\n")
	expectReply(t, reader, array(streamReply("second", entryReply("7-0", "b", "2"))))

	// The client stopped waiting on both streams.
	waitForBlockedReaders(t, srv, "first", 0)
	waitForBlockedReaders(t, srv, "second", 0)
}

func TestXReadBlockIgnoresEntriesBelowTheRequestedID(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	// The client only wants entries after 100-0.
	do(t, reader, "XREAD", "BLOCK", "300", "STREAMS", "stream_key", "100-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	// This entry wakes the client up, but isn't what it asked for: it keeps
	// waiting, and the timeout eventually fires.
	do(t, writer, "XADD", "stream_key", "50-0", "too", "early")
	expectReply(t, writer, "$4\r\n50-0\r\n")
	expectNoReply(t, reader, blockedWait)
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "100-1", "just", "right")
	expectReply(t, writer, "$5\r\n100-1\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("100-1", "just", "right"))))
}

func TestXReadBlockWaitingDoesNotExtendTheTimeout(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	start := time.Now()
	do(t, reader, "XREAD", "BLOCK", "200", "STREAMS", "stream_key", "100-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	// Wake-ups that don't satisfy the read must not restart the countdown.
	for i := 1; i <= 3; i++ {
		time.Sleep(40 * time.Millisecond)
		id := fmt.Sprintf("1-%d", i)
		do(t, writer, "XADD", "stream_key", id, "n", "x")
		expectReply(t, writer, bulk(id))
	}

	expectReply(t, reader, nullArrayReply)
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 600*time.Millisecond {
		t.Fatalf("XREAD timed out after %v, want about 200ms", elapsed)
	}
}

func TestXReadBlockDoesNotBlockOtherClients(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	other := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "2000", "STREAMS", "stream_key", "0-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, other, "PING")
	expectReply(t, other, "+PONG\r\n")
	do(t, other, "SET", "foo", "bar")
	expectReply(t, other, "+OK\r\n")
	// Entries added to other streams don't concern the blocked client.
	do(t, other, "XADD", "unrelated", "1-1", "foo", "bar")
	expectReply(t, other, "$3\r\n1-1\r\n")
	expectNoReply(t, reader, blockedWait)
}

func TestXReadBlockClientCanBlockAgain(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	// The usual way to consume a stream: read after the last ID received.
	last := "0-0"
	for _, id := range []string{"1-0", "2-0", "3-0"} {
		do(t, reader, "XREAD", "BLOCK", "2000", "STREAMS", "stream_key", last)
		waitForBlockedReaders(t, srv, "stream_key", 1)

		do(t, writer, "XADD", "stream_key", id, "id", id)
		expectReply(t, writer, bulk(id))
		expectReply(t, reader, array(streamReply("stream_key", entryReply(id, "id", id))))
		last = id
	}
}

func TestXReadBlockDisconnectedClientIsForgotten(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	do(t, reader, "XREAD", "BLOCK", "5000", "STREAMS", "stream_key", "0-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	reader.Close()
	waitForBlockedReaders(t, srv, "stream_key", 0)

	// Adding entries afterwards works as usual.
	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")
}

func TestXReadBlockOptionSyntax(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	want := array(streamReply("stream_key", entryReply("0-1", "foo", "bar")))

	// Option names are case-insensitive.
	for _, words := range [][2]string{{"BLOCK", "STREAMS"}, {"block", "streams"}, {"Block", "Streams"}} {
		do(t, conn, "XREAD", words[0], "100", words[1], "stream_key", "0-0")
		expectReply(t, conn, want)
	}

	do(t, conn, "XREAD", "BLOCK", "soon", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, "-ERR timeout is not an integer or out of range\r\n")
	do(t, conn, "XREAD", "BLOCK", "1.5", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, "-ERR timeout is not an integer or out of range\r\n")
	do(t, conn, "XREAD", "BLOCK", "-1", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, "-ERR timeout is negative\r\n")
	do(t, conn, "XREAD", "BLOCK", "+100", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, "-ERR timeout is not an integer or out of range\r\n")

	// BLOCK without STREAMS, or as the last word.
	do(t, conn, "XREAD", "BLOCK", "100", "stream_key", "0-0")
	expectReply(t, conn, "-ERR syntax error\r\n")
	do(t, conn, "XREAD", "STREAMS", "stream_key", "0-0", "BLOCK")
	expectReply(t, conn, unbalancedXReadErr)

	// Errors are reported right away, even though BLOCK was given.
	do(t, conn, "XREAD", "BLOCK", "5000", "STREAMS", "stream_key", "oops")
	expectReply(t, conn, invalidStreamIDError)
	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "XREAD", "BLOCK", "5000", "STREAMS", "text", "0-0")
	expectReply(t, conn, wrongTypeError)
}

func TestXReadBlockWithVeryLongTimeout(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	reader := dial(t, addr)
	writer := dial(t, addr)

	// Larger than what fits in a time.Duration once converted to nanoseconds.
	do(t, reader, "XREAD", "BLOCK", "9223372036854775807", "STREAMS", "stream_key", "0-0")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, writer, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, writer, "$3\r\n1-1\r\n")
	expectReply(t, reader, array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))))
}

func TestParseXReadArgs(t *testing.T) {
	valid := []struct {
		args []string
		want xreadArgs
	}{
		{
			[]string{"STREAMS", "a", "0-0"},
			xreadArgs{keys: []string{"a"}, ids: []string{"0-0"}},
		},
		{
			[]string{"streams", "a", "b", "1-0", "2-0"},
			xreadArgs{keys: []string{"a", "b"}, ids: []string{"1-0", "2-0"}},
		},
		{
			[]string{"BLOCK", "1500", "STREAMS", "a", "0-0"},
			xreadArgs{block: true, timeout: 1500 * time.Millisecond, keys: []string{"a"}, ids: []string{"0-0"}},
		},
		{
			[]string{"block", "0", "streams", "a", "0-0"},
			xreadArgs{block: true, keys: []string{"a"}, ids: []string{"0-0"}},
		},
		{
			// After STREAMS everything is a key or an ID, even "BLOCK".
			[]string{"STREAMS", "BLOCK", "0-0"},
			xreadArgs{keys: []string{"BLOCK"}, ids: []string{"0-0"}},
		},
	}
	for _, tt := range valid {
		got, errReply := parseXReadArgs(tt.args)
		if errReply != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseXReadArgs(%q) = (%+v, %q), want (%+v, nil)", tt.args, got, errReply, tt.want)
		}
	}

	invalid := [][]string{
		{"a", "0-0"},
		{"BLOCK", "100", "a", "0-0"},
		{"BLOCK", "100"},
		{"BLOCK"},
		{"STREAMS"},
		{"BLOCK", "x", "STREAMS", "a", "0-0"},
		{"BLOCK", "-5", "STREAMS", "a", "0-0"},
		{"STREAMS", "a", "b", "0-0"},
		{"UNKNOWN", "1", "STREAMS", "a", "0-0"},
	}
	for _, args := range invalid {
		if got, errReply := parseXReadArgs(args); errReply == nil {
			t.Errorf("parseXReadArgs(%q) = %+v, want an error reply", args, got)
		}
	}
}

func TestWaitQueues(t *testing.T) {
	q := make(waitQueues[string])

	q.add("first", []string{"a", "b"})
	q.add("second", []string{"a"})
	q.add("third", []string{"a", "c"})

	if want := []string{"first", "second", "third"}; !reflect.DeepEqual(q["a"], want) {
		t.Fatalf(`queue of "a" = %q, want %q (arrival order)`, q["a"], want)
	}

	// Removing from the middle keeps the order of the others.
	q.remove("second", []string{"a"})
	if want := []string{"first", "third"}; !reflect.DeepEqual(q["a"], want) {
		t.Fatalf(`queue of "a" = %q, want %q`, q["a"], want)
	}

	// Removing is a no-op for keys the waiter isn't queued on.
	q.remove("first", []string{"a", "b", "zzz"})
	q.remove("nobody", []string{"a"})
	if want := []string{"third"}; !reflect.DeepEqual(q["a"], want) {
		t.Fatalf(`queue of "a" = %q, want %q`, q["a"], want)
	}

	// Emptied queues disappear.
	if _, exists := q["b"]; exists {
		t.Fatalf(`empty queue of "b" was kept`)
	}
	q.remove("third", []string{"a", "c"})
	if len(q) != 0 {
		t.Fatalf("queues = %v, want none", q)
	}
}

func TestUnique(t *testing.T) {
	got := unique([]string{"b", "a", "b", "c", "a", "b"})
	if want := []string{"b", "a", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unique = %q, want %q", got, want)
	}
	if got := unique(nil); len(got) != 0 {
		t.Fatalf("unique(nil) = %q, want nothing", got)
	}
}
