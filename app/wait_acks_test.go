package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// getAckLength is the number of bytes of a propagated REPLCONF GETACK *.
var getAckLength = len(encodeCommand("REPLCONF", "GETACK", "*"))

// expectGetAck checks that the next thing the replica receives is a request
// for acknowledgement.
func expectGetAck(t *testing.T, replica net.Conn) {
	t.Helper()
	expectPropagated(t, replica, "REPLCONF", "GETACK", "*")
}

// ack makes the replica acknowledge the given offset.
func ack(t *testing.T, replica net.Conn, offset int) {
	t.Helper()
	do(t, replica, "REPLCONF", "ACK", strconv.Itoa(offset))
}

// elapsed runs f and returns how long it took.
func elapsed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

func TestWaitReturnsOnceEnoughReplicasAcknowledged(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "123")
	expectReply(t, conn, "+OK\r\n")
	offset := len(encodeCommand("SET", "foo", "123"))

	do(t, conn, "WAIT", "1", "5000")
	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "foo", "123")
		expectGetAck(t, replica)
	}
	// Nobody acknowledged yet: the client is kept waiting.
	expectNoReply(t, conn, 50*time.Millisecond)

	ack(t, replicas[0], offset)
	// One replica is all the client asked for; the timeout isn't waited for.
	expectReply(t, conn, ":1\r\n")
}

func TestWaitReturnsAtTimeoutWithTheReplicasThatAcknowledged(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "123")
	expectReply(t, conn, "+OK\r\n")
	offset := len(encodeCommand("SET", "foo", "123"))

	took := elapsed(func() {
		do(t, conn, "WAIT", "3", "300")
		for _, replica := range replicas[:2] {
			expectPropagated(t, replica, "SET", "foo", "123")
			expectGetAck(t, replica)
			ack(t, replica, offset)
		}
		// The third replica never answers.
		expectReply(t, conn, ":2\r\n")
	})
	if took < 250*time.Millisecond {
		t.Errorf("WAIT replied after %v, want it to wait for its timeout of 300ms", took)
	}
}

func TestWaitReturnsZeroWhenNoReplicaAcknowledges(t *testing.T) {
	addr := startTestServer(t)
	connectReplicas(t, addr, 2)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "123")
	expectReply(t, conn, "+OK\r\n")

	took := elapsed(func() {
		do(t, conn, "WAIT", "1", "200")
		expectReply(t, conn, ":0\r\n")
	})
	if took < 150*time.Millisecond {
		t.Errorf("WAIT replied after %v, want it to wait for its timeout of 200ms", took)
	}
}

func TestWaitCanReturnMoreReplicasThanAskedFor(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "123")
	expectReply(t, conn, "+OK\r\n")
	offset := len(encodeCommand("SET", "foo", "123"))

	// A first WAIT learns that every replica is up to date...
	do(t, conn, "WAIT", "3", "5000")
	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "foo", "123")
		expectGetAck(t, replica)
		ack(t, replica, offset)
	}
	expectReply(t, conn, ":3\r\n")

	// ...so asking for one of them afterwards finds three, right away and
	// without asking the replicas again.
	do(t, conn, "WAIT", "1", "5000")
	expectReply(t, conn, ":3\r\n")
	for _, replica := range replicas {
		expectNoReply(t, replica, 20*time.Millisecond)
	}
}

func TestWaitAfterEachWrite(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	// What each replica has received so far.
	offset := 0

	do(t, conn, "SET", "foo", "123")
	expectReply(t, conn, "+OK\r\n")
	offset += len(encodeCommand("SET", "foo", "123"))

	do(t, conn, "WAIT", "1", "5000")
	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "foo", "123")
		expectGetAck(t, replica)
	}
	ack(t, replicas[0], offset)
	expectReply(t, conn, ":1\r\n")
	offset += getAckLength

	do(t, conn, "SET", "bar", "456")
	expectReply(t, conn, "+OK\r\n")
	offset += len(encodeCommand("SET", "bar", "456"))

	do(t, conn, "WAIT", "2", "5000")
	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "bar", "456")
		expectGetAck(t, replica)
	}
	ack(t, replicas[1], offset)
	expectNoReply(t, conn, 50*time.Millisecond)
	ack(t, replicas[2], offset)
	expectReply(t, conn, ":2\r\n")
}

func TestWaitIgnoresAcknowledgementsOfEarlierOffsets(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, "+OK\r\n")
	first := len(encodeCommand("SET", "foo", "1"))

	do(t, conn, "WAIT", "1", "5000")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectPropagated(t, replica, "SET", "bar", "2")
	expectGetAck(t, replica)

	// The replica only got as far as the first write.
	ack(t, replica, first)
	expectNoReply(t, conn, 50*time.Millisecond)

	ack(t, replica, 2*first)
	expectReply(t, conn, ":1\r\n")
}

func TestWaitCountsAReplicaThatIsAhead(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "WAIT", "1", "5000")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectGetAck(t, replica)
	// The acknowledgement covers more than the write: it counts.
	ack(t, replica, 1000)
	expectReply(t, conn, ":1\r\n")
}

func TestWaitDoesNotWaitForWritesMadeMeanwhile(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)
	other := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	offset := len(encodeCommand("SET", "foo", "1"))

	do(t, conn, "WAIT", "1", "5000")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectGetAck(t, replica)

	// Another client writes while the first one waits.
	do(t, other, "SET", "bar", "2")
	expectReply(t, other, "+OK\r\n")
	expectPropagated(t, replica, "SET", "bar", "2")

	// Acknowledging the first write is enough for the client that waits.
	ack(t, replica, offset)
	expectReply(t, conn, ":1\r\n")
}

func TestSeveralClientsWaitAtTheSameTime(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 2)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "SET", "foo", "1")
	expectReply(t, first, "+OK\r\n")
	offset := len(encodeCommand("SET", "foo", "1"))

	do(t, first, "WAIT", "1", "5000")
	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "foo", "1")
		expectGetAck(t, replica)
	}
	do(t, second, "WAIT", "2", "5000")
	for _, replica := range replicas {
		expectGetAck(t, replica)
	}

	ack(t, replicas[0], offset)
	expectReply(t, first, ":1\r\n")
	expectNoReply(t, second, 50*time.Millisecond)

	ack(t, replicas[1], offset)
	expectReply(t, second, ":2\r\n")
}

func TestWaitWithZeroTimeoutWaitsForAsLongAsItTakes(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "WAIT", "1", "0")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectGetAck(t, replica)
	expectNoReply(t, conn, 200*time.Millisecond)

	ack(t, replica, len(encodeCommand("SET", "foo", "1")))
	expectReply(t, conn, ":1\r\n")
}

func TestWaitForZeroReplicasNeverWaits(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	expectPropagated(t, replica, "SET", "foo", "1")

	do(t, conn, "WAIT", "0", "5000")
	expectReply(t, conn, ":0\r\n")
	// The replicas aren't even asked.
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestWaitAfterATransaction(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "MULTI")
	do(t, conn, "SET", "foo", "1")
	do(t, conn, "INCR", "foo")
	do(t, conn, "EXEC")
	drainPendingReply(t, conn)

	stream := encodeCommand("MULTI") + encodeCommand("SET", "foo", "1") + encodeCommand("INCR", "foo") + encodeCommand("EXEC")
	expectReply(t, replica, stream)

	do(t, conn, "WAIT", "1", "5000")
	expectGetAck(t, replica)
	ack(t, replica, len(stream)-1)
	expectNoReply(t, conn, 50*time.Millisecond)
	ack(t, replica, len(stream))
	expectReply(t, conn, ":1\r\n")
}

func TestWaitInsideATransactionDoesNotBlock(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	expectPropagated(t, replica, "SET", "foo", "1")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WAIT", "1", "5000")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n"))
}

func TestWaitIsInterruptedWhenTheClientDisconnects(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WAIT", "1", "0")

	waiters := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.ackWaiters)
	}
	eventually(t, "the client to be waiting", func() bool { return waiters() == 1 })

	conn.Close()
	eventually(t, "the wait to be abandoned", func() bool { return waiters() == 0 })
}

func TestAcknowledgementsAreNotAnsweredAndOnlyCountFromReplicas(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)
	impostor := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "WAIT", "1", "300")
	expectPropagated(t, replica, "SET", "foo", "1")
	expectGetAck(t, replica)

	// A client that isn't a replica can't acknowledge anything.
	ack(t, impostor, 1000)
	expectNoReply(t, impostor, 50*time.Millisecond)
	expectReply(t, conn, ":0\r\n")

	// Nothing is sent back to a replica that acknowledges either.
	ack(t, replica, len(encodeCommand("SET", "foo", "1")))
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestWaitOnAReplicaIsAnError(t *testing.T) {
	conn := dial(t, startReplicaTestServer(t))

	do(t, conn, "WAIT", "0", "100")
	if line := readReplyLine(t, conn); !strings.HasPrefix(line, "-ERR WAIT cannot be used with replica instances") {
		t.Errorf("reply = %q, want an error saying WAIT can't be used on a replica", line)
	}
}

// TestWaitWithRealReplicas uses replicas that are the server under test too:
// they answer GETACK by themselves.
func TestWaitWithRealReplicas(t *testing.T) {
	master := NewServer()
	masterAddr := startServer(t, master)
	replicaAddrs := []string{
		startReplicaOfServer(t, master, masterAddr),
		startReplicaOfServer(t, master, masterAddr),
		startReplicaOfServer(t, master, masterAddr),
	}
	conn := dial(t, masterAddr)

	do(t, conn, "WAIT", "3", "500")
	expectReply(t, conn, ":3\r\n")

	for i := range 5 {
		value := strconv.Itoa(i)
		do(t, conn, "SET", "foo", value)
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, "WAIT", "3", "2000")
		expectReply(t, conn, ":3\r\n")

		// A replica that acknowledged the write has applied it.
		for _, addr := range replicaAddrs {
			onReplica := dial(t, addr)
			do(t, onReplica, "GET", "foo")
			expectReply(t, onReplica, "$1\r\n"+value+"\r\n")
		}
	}
}
