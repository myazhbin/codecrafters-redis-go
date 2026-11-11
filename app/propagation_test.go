package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// connectReplica connects to the master at addr the way a replica does: it
// goes through the whole handshake and receives the RDB file. What the master
// sends on the returned connection afterwards is the replication stream.
func connectReplica(t *testing.T, addr string) net.Conn {
	t.Helper()

	conn := dial(t, addr)
	handshakeUntilPsync(t, conn)
	do(t, conn, "PSYNC", "?", "-1")
	if line := readReplyLine(t, conn); !strings.HasPrefix(line, "+FULLRESYNC ") {
		t.Fatalf("reply to PSYNC = %q, want +FULLRESYNC", line)
	}
	receiveRDBTransfer(t, conn)
	return conn
}

// expectPropagated checks that the next thing in the replication stream is
// the given command, encoded as a RESP array.
func expectPropagated(t *testing.T, replica net.Conn, command ...string) {
	t.Helper()
	expectReply(t, replica, encodeCommand(command...))
}

func TestWritesArePropagatedToTheReplica(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "baz", "3")
	expectReply(t, conn, "+OK\r\n")

	// The commands arrive on the connection of the handshake, as RESP
	// arrays, in the order they were executed.
	expectReply(t, replica, "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$1\r\n1\r\n")
	expectPropagated(t, replica, "SET", "bar", "2")
	expectPropagated(t, replica, "SET", "baz", "3")
}

func TestWritesFromSeveralClientsArePropagatedInOrder(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)

	for i := range 5 {
		// One connection per command, like redis-cli does.
		conn := dial(t, addr)
		do(t, conn, "SET", "key", strconv.Itoa(i))
		expectReply(t, conn, "+OK\r\n")
	}
	for i := range 5 {
		expectPropagated(t, replica, "SET", "key", strconv.Itoa(i))
	}
}

func TestReadsAreNotPropagated(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	do(t, conn, "RPUSH", "list_key", "a")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	for _, command := range [][]string{
		{"PING"},
		{"ECHO", "hey"},
		{"GET", "foo"},
		{"TYPE", "foo"},
		{"LRANGE", "list_key", "0", "-1"},
		{"LLEN", "list_key"},
		{"XRANGE", "stream_key", "-", "+"},
		{"XREAD", "STREAMS", "stream_key", "0-0"},
		{"INFO", "replication"},
		{"WATCH", "foo"},
		{"UNWATCH"},
		{"REPLCONF", "capa", "psync2"},
	} {
		do(t, conn, command...)
	}
	do(t, conn, "SET", "end", "1")
	drainPendingReply(t, conn)

	// Only the writes show up in the stream.
	expectPropagated(t, replica, "SET", "foo", "1")
	expectPropagated(t, replica, "RPUSH", "list_key", "a")
	expectPropagated(t, replica, "XADD", "stream_key", "1-1", "k", "v")
	expectPropagated(t, replica, "SET", "end", "1")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestFailedWritesAreNotPropagated(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "text", "abc")
	do(t, conn, "RPUSH", "list_key", "a")
	do(t, conn, "XADD", "stream_key", "5-5", "k", "v")
	for _, command := range [][]string{
		{"INCR", "text"},                        // not an integer
		{"INCR", "list_key"},                    // wrong type
		{"RPUSH", "text", "a"},                  // wrong type
		{"LPOP", "text"},                        // wrong type
		{"LPOP", "missing"},                     // nothing to pop
		{"LPOP", "missing", "2"},                // nothing to pop
		{"LPOP", "list_key", "0"},               // pops nothing
		{"XADD", "stream_key", "1-1", "k", "v"}, // ID too small
		{"SET", "foo", "1", "PX", "abc"},        // invalid expiry
		{"SET", "foo"},                          // wrong arity
		{"BLPOP", "missing", "0.01"},            // times out
		{"NOSUCHCOMMAND", "foo"},
	} {
		do(t, conn, command...)
	}
	time.Sleep(50 * time.Millisecond) // let BLPOP time out
	do(t, conn, "SET", "end", "1")
	drainPendingReply(t, conn)

	expectPropagated(t, replica, "SET", "text", "abc")
	expectPropagated(t, replica, "RPUSH", "list_key", "a")
	expectPropagated(t, replica, "XADD", "stream_key", "5-5", "k", "v")
	expectPropagated(t, replica, "SET", "end", "1")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestEveryKindOfWriteIsPropagated(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	tests := []struct {
		command []string
		// want is what the replica receives; nil means the command itself.
		want []string
	}{
		{command: []string{"SET", "foo", "1"}},
		{command: []string{"SET", "ttl", "v", "PX", "100000"}},
		{command: []string{"set", "lower", "v"}, want: []string{"SET", "lower", "v"}},
		{command: []string{"INCR", "foo"}},
		{command: []string{"INCR", "new_counter"}},
		{command: []string{"RPUSH", "list_key", "a", "b", "c", "d"}},
		{command: []string{"LPUSH", "list_key", "z"}},
		{command: []string{"LPOP", "list_key"}},
		{command: []string{"LPOP", "list_key", "2"}},
		// A replica never waits: a blocking pop reaches it as a plain one.
		{command: []string{"BLPOP", "list_key", "1"}, want: []string{"LPOP", "list_key"}},
		{command: []string{"XADD", "stream_key", "1-1", "k", "v"}},
		// Generated IDs are propagated as they were resolved.
		{command: []string{"XADD", "stream_key", "1-*", "k", "v"}, want: []string{"XADD", "stream_key", "1-2", "k", "v"}},
	}
	for _, tt := range tests {
		do(t, conn, tt.command...)
		drainPendingReply(t, conn)

		want := tt.want
		if want == nil {
			want = tt.command
		}
		expectPropagated(t, replica, want...)
	}
}

func TestXaddWithFullyGeneratedIDIsPropagatedWithItsID(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "XADD", "stream_key", "*", "k", "v")
	id := readBulkString(t, conn)
	if id == "*" || !strings.Contains(id, "-") {
		t.Fatalf("XADD replied with the ID %q", id)
	}
	expectPropagated(t, replica, "XADD", "stream_key", id, "k", "v")
}

func TestBlockedPopIsPropagatedAfterThePushThatServedIt(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	blocked := dial(t, addr)
	conn := dial(t, addr)

	do(t, blocked, "BLPOP", "list_key", "0")
	expectNoReply(t, blocked, 50*time.Millisecond)
	// Waiting changes nothing, so nothing is propagated yet.
	expectNoReply(t, replica, 50*time.Millisecond)

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	expectReply(t, blocked, bulkArray("list_key", "a"))

	// The replica ends up with the same list as the master: [b].
	expectPropagated(t, replica, "RPUSH", "list_key", "a", "b")
	expectPropagated(t, replica, "LPOP", "list_key")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestNothingIsPropagatedBeforeTheReplicaConnects(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "before", "1")
	expectReply(t, conn, "+OK\r\n")

	replica := connectReplica(t, addr)
	do(t, conn, "SET", "after", "1")
	expectReply(t, conn, "+OK\r\n")

	// The stream starts when the replica finishes the handshake.
	expectPropagated(t, replica, "SET", "after", "1")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestNothingIsPropagatedToRegularClients(t *testing.T) {
	addr := startTestServer(t)
	connectReplica(t, addr)
	bystander := dial(t, addr)
	conn := dial(t, addr)

	// Half a handshake doesn't make a replica.
	handshakeUntilPsync(t, bystander)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	expectNoReply(t, bystander, 50*time.Millisecond)
	expectNoReply(t, conn, 50*time.Millisecond)
}

func TestTransactionIsPropagatedAsATransaction(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, queued)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, queued)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	// Queued commands haven't changed anything yet.
	expectNoReply(t, replica, 50*time.Millisecond)

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", "$1\r\n1\r\n", ":2\r\n"))

	// The writes, and only them, reach the replica as one transaction.
	expectPropagated(t, replica, "MULTI")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectPropagated(t, replica, "INCR", "foo")
	expectPropagated(t, replica, "EXEC")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestTransactionsThatWriteNothingAreNotPropagated(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	// Empty, read-only, discarded and aborted transactions.
	do(t, conn, "MULTI")
	do(t, conn, "EXEC")
	do(t, conn, "MULTI")
	do(t, conn, "GET", "foo")
	do(t, conn, "EXEC")
	do(t, conn, "MULTI")
	do(t, conn, "SET", "foo", "1")
	do(t, conn, "DISCARD")
	do(t, conn, "WATCH", "watched")
	do(t, conn, "SET", "watched", "1")
	do(t, conn, "MULTI")
	do(t, conn, "SET", "foo", "2")
	do(t, conn, "EXEC")
	drainPendingReply(t, conn)

	expectPropagated(t, replica, "SET", "watched", "1")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestReplicaConnectionStillGetsRepliesToItsCommands(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)

	do(t, replica, "PING")
	expectReply(t, replica, "+PONG\r\n")
	do(t, replica, "REPLCONF", "capa", "psync2")
	expectReply(t, replica, "+OK\r\n")
}

func TestPropagationSurvivesAReplicaThatDisconnects(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	replica.Close()
	eventually(t, "the master to forget the replica", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.replicas) == 0
	})

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n1\r\n")
}

func TestMasterIsNotHeldUpByAReplicaThatDoesNotRead(t *testing.T) {
	addr := startTestServer(t)
	connectReplica(t, addr) // never reads the stream
	conn := dial(t, addr)

	// Far more than the connection's buffers can hold.
	value := strings.Repeat("x", 64*1024)
	for range 200 {
		do(t, conn, "SET", "big", value)
		expectReply(t, conn, "+OK\r\n")
	}
}

func TestPsyncInsideATransactionIsRejected(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "PSYNC", "?", "-1")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array("-ERR PSYNC is not allowed in a transaction\r\n"))
}

func TestOutboxWritesInOrderAndStopsWhenClosed(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	o := newOutbox()
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.run(server)
	}()

	o.push([]byte("one "))
	o.push([]byte("two "))
	o.push([]byte("three"))

	want := "one two three"
	got := make([]byte, len(want))
	client.SetReadDeadline(time.Now().Add(replyTimeout))
	for n := 0; n < len(got); {
		m, err := client.Read(got[n:])
		if err != nil {
			t.Fatalf("reading: %v (got %q so far)", err, got[:n])
		}
		n += m
	}
	if string(got) != want {
		t.Errorf("read %q, want %q", got, want)
	}

	o.close()
	select {
	case <-done:
	case <-time.After(replyTimeout):
		t.Fatalf("run didn't return after close")
	}
	// Pushing to a closed outbox is harmless.
	o.push([]byte("dropped"))
}
