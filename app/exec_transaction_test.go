package main

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExecRunsQueuedCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "6")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "bar")
	expectReply(t, conn, queued)
	do(t, conn, "GET", "bar")
	expectReply(t, conn, queued)

	// One reply per queued command, in the order they were queued.
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":7\r\n", ":1\r\n", "$1\r\n1\r\n"))

	// The transaction was executed for real.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n7\r\n")
}

func TestExecExactEncoding(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	do(t, conn, "EXEC")
	expectReply(t, conn, "*2\r\n+OK\r\n:42\r\n")
}

func TestExecEffectsAreVisibleToOtherClients(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, queued)
	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, queued)

	do(t, other, "GET", "foo")
	expectReply(t, other, "$-1\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":2\r\n"))

	do(t, other, "GET", "foo")
	expectReply(t, other, "$2\r\n41\r\n")
	do(t, other, "LRANGE", "list_key", "0", "-1")
	expectReply(t, other, bulkArray("a", "b"))
}

func TestExecRepliesOfEveryKind(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	commands := [][]string{
		{"PING"},
		{"ECHO", "hey"},
		{"GET", "missing"},
		{"RPUSH", "list_key", "a", "b", "c"},
		{"LRANGE", "list_key", "0", "-1"},
		{"LPOP", "list_key", "2"},
		{"LPOP", "missing", "2"},
		{"XADD", "stream_key", "1-1", "foo", "bar"},
		{"XRANGE", "stream_key", "-", "+"},
		{"XREAD", "STREAMS", "stream_key", "0-0"},
		{"TYPE", "stream_key"},
		{"LRANGE", "missing", "0", "-1"},
	}
	for _, command := range commands {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}

	// Each reply keeps its own type, including arrays nested in the array.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(
		"+PONG\r\n",
		"$3\r\nhey\r\n",
		"$-1\r\n",
		":3\r\n",
		bulkArray("a", "b", "c"),
		bulkArray("a", "b"),
		"*-1\r\n",
		"$3\r\n1-1\r\n",
		array(entryReply("1-1", "foo", "bar")),
		array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))),
		"+stream\r\n",
		"*0\r\n",
	))
}

func TestExecRunsCommandsInOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	// Each command sees the effects of the ones queued before it.
	for _, command := range [][]string{
		{"GET", "key"},
		{"SET", "key", "first"},
		{"GET", "key"},
		{"SET", "key", "second"},
		{"GET", "key"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}

	do(t, conn, "EXEC")
	expectReply(t, conn, array("$-1\r\n", "+OK\r\n", "$5\r\nfirst\r\n", "+OK\r\n", "$6\r\nsecond\r\n"))
}

func TestExecUsesTheDataAsOfExecTime(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, queued)

	// These happen after the commands were queued, but before they run.
	do(t, other, "SET", "foo", "set-meanwhile")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "SET", "counter", "100")
	expectReply(t, other, "+OK\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("$13\r\nset-meanwhile\r\n", ":101\r\n"))
}

func TestExecEndsTheTransactionAfterRunningIt(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n"))

	// Commands run directly again, and the queue isn't replayed.
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, execWithoutMultiError)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$1\r\n2\r\n")
}

func TestExecManyCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const count = 300
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	replies := make([]string, count)
	for i := range replies {
		do(t, conn, "INCR", "counter")
		expectReply(t, conn, queued)
		replies[i] = ":" + strconv.Itoa(i+1) + "\r\n"
	}

	do(t, conn, "EXEC")
	expectReply(t, conn, array(replies...))
}

func TestExecTransactionsOfTwoClientsDoNotMix(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")

	// The two clients queue their commands in alternation.
	do(t, first, "SET", "key", "from-first")
	expectReply(t, first, queued)
	do(t, second, "SET", "key", "from-second")
	expectReply(t, second, queued)
	do(t, first, "GET", "key")
	expectReply(t, first, queued)
	do(t, second, "GET", "key")
	expectReply(t, second, queued)

	// Each EXEC runs its own queue only, as one block.
	do(t, second, "EXEC")
	expectReply(t, second, array("+OK\r\n", "$11\r\nfrom-second\r\n"))
	do(t, first, "EXEC")
	expectReply(t, first, array("+OK\r\n", "$10\r\nfrom-first\r\n"))
}

func TestExecBlockingCommandsDoNotBlock(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "ready", "element")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"BLPOP", "ready", "0"},
		{"BLPOP", "empty", "0"},
		{"BLPOP", "empty", "5"},
		{"XREAD", "BLOCK", "0", "STREAMS", "stream_key", "0-0"},
		{"XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$"},
		{"XREAD", "BLOCK", "5000", "STREAMS", "missing", "0-0"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, queued)
	}

	// Inside a transaction blocking commands return what's available, or
	// nothing, right away.
	start := time.Now()
	do(t, conn, "EXEC")
	expectReply(t, conn, array(
		bulkArray("ready", "element"),
		"*-1\r\n",
		"*-1\r\n",
		array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))),
		"*-1\r\n",
		"*-1\r\n",
	))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("EXEC took %v: a blocking command blocked", elapsed)
	}
}

func TestExecUnblocksClientsWaitingForItsWrites(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)
	listWaiter := dial(t, addr)
	streamWaiter := dial(t, addr)

	do(t, listWaiter, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)
	do(t, streamWaiter, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	waitForBlockedReaders(t, srv, "stream_key", 1)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "pushed")
	expectReply(t, conn, queued)
	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, queued)

	// Queueing alone doesn't wake anybody up.
	expectNoReply(t, listWaiter, blockedWait)
	expectNoReply(t, streamWaiter, blockedWait)

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", "$3\r\n1-1\r\n"))
	expectReply(t, listWaiter, bulkArray("list_key", "pushed"))
	expectReply(t, streamWaiter, array(streamReply("stream_key", entryReply("1-1", "foo", "bar"))))
}

// TestExecIsAtomic checks that other clients never observe a transaction
// halfway through: a counter that is always incremented twice per transaction
// must never be seen with an odd value.
func TestExecIsAtomic(t *testing.T) {
	addr := startTestServer(t)
	writer := dial(t, addr)
	reader := dial(t, addr)

	const transactions = 200

	done := make(chan error, 1)
	go func() {
		r := bufio.NewReader(writer)
		for i := 0; i < transactions; i++ {
			batch := encodeCommand("MULTI") + encodeCommand("INCR", "counter") +
				encodeCommand("INCR", "counter") + encodeCommand("EXEC")
			if _, err := writer.Write([]byte(batch)); err != nil {
				done <- fmt.Errorf("writer: %w", err)
				return
			}
			// +OK, +QUEUED, +QUEUED, *2, :n, :n+1
			for line := 0; line < 6; line++ {
				if _, err := r.ReadString('\n'); err != nil {
					done <- fmt.Errorf("writer: reading replies: %w", err)
					return
				}
			}
		}
		done <- nil
	}()

	r := bufio.NewReader(reader)
	for finished := false; !finished; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			finished = true
		default:
		}

		if _, err := reader.Write([]byte(encodeCommand("GET", "counter"))); err != nil {
			t.Fatalf("reader: write: %v", err)
		}
		header, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reader: read: %v", err)
		}
		if header == "$-1\r\n" {
			continue // no transaction ran yet
		}
		value, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reader: read: %v", err)
		}
		n, err := strconv.Atoi(strings.TrimSuffix(value, "\r\n"))
		if err != nil {
			t.Fatalf("reader: counter holds %q", value)
		}
		if n%2 != 0 {
			t.Fatalf("observed the counter at %d, in the middle of a transaction", n)
		}
	}

	do(t, reader, "GET", "counter")
	expectReply(t, reader, bulk(strconv.Itoa(2*transactions)))
}
