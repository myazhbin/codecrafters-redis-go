package main

import (
	"strconv"
	"testing"
)

func TestWaitCountsConnectedReplicasWhenNothingWasWritten(t *testing.T) {
	addr := startTestServer(t)
	connectReplicas(t, addr, 7)
	conn := dial(t, addr)

	// Every replica is in sync, however many the client asks for.
	for _, numReplicas := range []string{"3", "7", "9", "0"} {
		do(t, conn, "WAIT", numReplicas, "500")
		expectReply(t, conn, ":7\r\n")
	}
}

func TestWaitCountFollowsTheNumberOfReplicas(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)

	for n := 1; n <= 4; n++ {
		connectReplica(t, addr)
		do(t, conn, "WAIT", "1", "500")
		expectReply(t, conn, ":"+strconv.Itoa(n)+"\r\n")
	}
}

func TestWaitDoesNotCountDisconnectedReplicas(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	replicas[0].Close()
	eventually(t, "the master to forget the disconnected replica", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.replicas) == 2
	})

	do(t, conn, "WAIT", "3", "500")
	expectReply(t, conn, ":2\r\n")
}

func TestWaitDoesNotCountClientsThatAreNotReplicas(t *testing.T) {
	addr := startTestServer(t)
	connectReplicas(t, addr, 2)
	conn := dial(t, addr)

	// Regular clients, and one that stopped in the middle of the handshake.
	dial(t, addr)
	halfway := dial(t, addr)
	handshakeUntilPsync(t, halfway)

	do(t, conn, "WAIT", "5", "500")
	expectReply(t, conn, ":2\r\n")
}

func TestWaitFromSeveralClients(t *testing.T) {
	addr := startTestServer(t)
	connectReplicas(t, addr, 3)

	for range 3 {
		conn := dial(t, addr)
		do(t, conn, "WAIT", "3", "500")
		expectReply(t, conn, ":3\r\n")
	}
}

func TestWaitCountsReplicasThatConnectedAfterWrites(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)

	early := connectReplica(t, addr)
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	expectPropagated(t, early, "SET", "foo", "1")

	// The snapshot a replica starts from includes everything written before
	// it connected: it has nothing to catch up with.
	// The early replica hasn't acknowledged the write, so only the late one
	// is known to be in sync.
	connectReplica(t, addr)
	do(t, conn, "WAIT", "0", "0")
	expectReply(t, conn, ":1\r\n")
}
