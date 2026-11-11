package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

// fullResync is what a fake master answers to PSYNC: the FULLRESYNC line
// followed by an empty RDB file.
func fullResync() string {
	return "+FULLRESYNC " + fakeReplID + " 0\r\n" + string(rdbTransfer(emptyRDB()))
}

// acceptReplica accepts the replica's connection and plays the master's part
// of the whole handshake, RDB file included. The replica is then ready to
// receive propagated commands.
func (m *fakeMaster) acceptReplica() *masterSide {
	m.t.Helper()

	link := m.acceptHandshakeUntilPsync()
	link.reply(fullResync())
	return link
}

// propagate sends commands to the replica the way a master propagates them.
func (m *masterSide) propagate(commands ...[]string) {
	m.t.Helper()

	var b strings.Builder
	for _, command := range commands {
		b.WriteString(encodeCommand(command...))
	}
	m.reply(b.String())
}

// expectEventually sends the command to the server until the reply is the
// one expected: replicas apply what their master sends them asynchronously.
// The expected reply must be a single line or a bulk string.
func expectEventually(t *testing.T, conn net.Conn, want string, command ...string) {
	t.Helper()

	deadline := time.Now().Add(replyTimeout)
	for {
		do(t, conn, command...)
		got := readReplyLine(t, conn) + "\r\n"
		if strings.HasPrefix(got, "$") && got != "$-1\r\n" {
			got += readReplyLine(t, conn) + "\r\n"
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reply to %q is still %q, want %q", command, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReplicaAppliesPropagatedCommands(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"SET", "foo", "1"})
	link.propagate([]string{"SET", "bar", "2"})
	link.propagate([]string{"SET", "baz", "3"})

	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	expectEventually(t, conn, "$1\r\n2\r\n", "GET", "bar")
	expectEventually(t, conn, "$1\r\n3\r\n", "GET", "baz")
}

func TestReplicaDoesNotReplyToPropagatedCommands(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate(
		[]string{"SET", "foo", "1"},
		[]string{"INCR", "foo"},
		[]string{"PING"},
		[]string{"GET", "foo"},
		[]string{"INCR", "text"},
		[]string{"NOSUCHCOMMAND"},
		[]string{"SET", "done", "1"},
	)

	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "done")
	// Whatever the command and its outcome, the master hears nothing back.
	link.expectSilence(100 * time.Millisecond)
}

func TestReplicaAppliesCommandsSentAlongWithTheHandshakeReply(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)

	// The reply to PSYNC, the RDB file and the first commands all arrive in
	// a single write.
	link := master.acceptHandshakeUntilPsync()
	link.reply(fullResync() + encodeCommand("SET", "foo", "1") + encodeCommand("SET", "bar", "2"))

	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	expectEventually(t, conn, "$1\r\n2\r\n", "GET", "bar")
}

func TestReplicaAppliesCommandsSplitAcrossWrites(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	// A command doesn't have to arrive in one piece.
	stream := encodeCommand("SET", "foo", "123") + encodeCommand("SET", "bar", "456")
	for _, chunk := range []string{stream[:7], stream[7:20], stream[20:41], stream[41:]} {
		link.reply(chunk)
		time.Sleep(5 * time.Millisecond)
	}

	conn := dial(t, addr)
	expectEventually(t, conn, "$3\r\n123\r\n", "GET", "foo")
	expectEventually(t, conn, "$3\r\n456\r\n", "GET", "bar")
}

func TestReplicaAppliesCommandsInOrder(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate(
		[]string{"SET", "foo", "1"},
		[]string{"SET", "foo", "2"},
		[]string{"INCR", "foo"},
		[]string{"RPUSH", "list_key", "a", "b", "c"},
		[]string{"LPOP", "list_key"},
		[]string{"XADD", "stream_key", "1-1", "k", "v"},
		[]string{"SET", "done", "1"},
	)

	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "done")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n3\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("b", "c"))
	do(t, conn, "XRANGE", "stream_key", "-", "+")
	expectReply(t, conn, array(entryReply("1-1", "k", "v")))
}

func TestReplicaAppliesPropagatedTransactions(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate(
		[]string{"MULTI"},
		[]string{"SET", "foo", "1"},
		[]string{"INCR", "foo"},
	)
	conn := dial(t, addr)
	// The transaction isn't applied before its EXEC arrives.
	time.Sleep(50 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")

	link.propagate([]string{"EXEC"})
	expectEventually(t, conn, "$1\r\n2\r\n", "GET", "foo")
	link.expectSilence(50 * time.Millisecond)
}

func TestReplicaWakesItsBlockedClients(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	conn := dial(t, addr)
	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	expectNoReply(t, conn, 50*time.Millisecond)

	link.propagate([]string{"XADD", "stream_key", "1-1", "k", "v"})
	expectReply(t, conn, array(streamReply("stream_key", entryReply("1-1", "k", "v"))))
}

func TestReplicaStopsFollowingOnProtocolError(t *testing.T) {
	master := startFakeMaster(t)
	cfg := defaultConfig()
	cfg.replicaOf = master.address()

	done := make(chan error, 1)
	go func() { done <- NewServerWithConfig(cfg).followMaster() }()

	link := master.acceptReplica()
	link.reply("*1\r\n+not a bulk string\r\n")

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "Protocol error") {
			t.Errorf("followMaster returned %v, want a protocol error", err)
		}
	case <-time.After(replyTimeout):
		t.Fatalf("followMaster didn't give up")
	}
}

func TestReplicaFailsWhenTheSnapshotIsMissing(t *testing.T) {
	for name, afterFullResync := range map[string]string{
		"not a file":      "+OK\r\n",
		"invalid length":  "$abc\r\n",
		"negative length": "$-1\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			master := startFakeMaster(t)
			cfg := defaultConfig()
			cfg.replicaOf = master.address()

			done := make(chan error, 1)
			go func() { done <- NewServerWithConfig(cfg).followMaster() }()

			link := master.acceptHandshakeUntilPsync()
			link.reply("+FULLRESYNC " + fakeReplID + " 0\r\n" + afterFullResync)

			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "receiving the snapshot") {
					t.Errorf("followMaster returned %v, want an error about the snapshot", err)
				}
			case <-time.After(replyTimeout):
				t.Fatalf("followMaster didn't give up")
			}
		})
	}
}

func TestFollowMasterEndsCleanlyWhenTheMasterClosesTheConnection(t *testing.T) {
	master := startFakeMaster(t)
	cfg := defaultConfig()
	cfg.replicaOf = master.address()

	done := make(chan error, 1)
	go func() { done <- NewServerWithConfig(cfg).followMaster() }()

	link := master.acceptReplica()
	link.propagate([]string{"SET", "foo", "1"})
	link.conn.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("followMaster returned %v, want no error", err)
		}
	case <-time.After(replyTimeout):
		t.Fatalf("followMaster didn't return after the master closed the connection")
	}
}

// startReplicaOfServer runs a replica of the master listening at masterAddr,
// which is the server under test too, and waits for it to be connected. It
// returns the address the replica's clients dial.
func startReplicaOfServer(t *testing.T, master *Server, masterAddr string) string {
	t.Helper()

	host, port, err := net.SplitHostPort(masterAddr)
	if err != nil {
		t.Fatalf("splitting the master's address: %v", err)
	}
	cfg, err := parseConfig([]string{"--replicaof", host + " " + port})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	replica := NewServerWithConfig(cfg)
	addr := startServer(t, replica)

	master.mu.Lock()
	before := len(master.replicas)
	master.mu.Unlock()

	go replica.followMaster()

	// Writes are only propagated to the replica once its handshake is over.
	eventually(t, "the replica to finish its handshake", func() bool {
		master.mu.Lock()
		defer master.mu.Unlock()
		return len(master.replicas) == before+1
	})
	return addr
}

// TestReplicaFollowsARealMaster connects a replica to a master that is the
// server under test too: what is written to one can be read from the other.
func TestReplicaFollowsARealMaster(t *testing.T) {
	master := NewServer()
	masterAddr := startServer(t, master)
	replicaAddr := startReplicaOfServer(t, master, masterAddr)

	onMaster := dial(t, masterAddr)
	onReplica := dial(t, replicaAddr)

	do(t, onMaster, "SET", "foo", "1")
	expectReply(t, onMaster, "+OK\r\n")
	do(t, onMaster, "INCR", "foo")
	expectReply(t, onMaster, ":2\r\n")
	do(t, onMaster, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, onMaster, ":3\r\n")
	do(t, onMaster, "BLPOP", "list_key", "1")
	expectReply(t, onMaster, bulkArray("list_key", "a"))
	do(t, onMaster, "XADD", "stream_key", "*", "k", "v")
	id := readBulkString(t, onMaster)
	do(t, onMaster, "MULTI")
	expectReply(t, onMaster, "+OK\r\n")
	do(t, onMaster, "INCR", "foo")
	expectReply(t, onMaster, queued)
	do(t, onMaster, "SET", "done", "1")
	expectReply(t, onMaster, queued)
	do(t, onMaster, "EXEC")
	expectReply(t, onMaster, array(":3\r\n", "+OK\r\n"))

	expectEventually(t, onReplica, "$1\r\n1\r\n", "GET", "done")
	do(t, onReplica, "GET", "foo")
	expectReply(t, onReplica, "$1\r\n3\r\n")
	do(t, onReplica, "LRANGE", "list_key", "0", "-1")
	expectReply(t, onReplica, bulkArray("b", "c"))
	do(t, onReplica, "XRANGE", "stream_key", "-", "+")
	expectReply(t, onReplica, array(entryReply(id, "k", "v")))
}

func TestSeveralReplicasFollowARealMaster(t *testing.T) {
	master := NewServer()
	masterAddr := startServer(t, master)
	replicaAddrs := []string{
		startReplicaOfServer(t, master, masterAddr),
		startReplicaOfServer(t, master, masterAddr),
		startReplicaOfServer(t, master, masterAddr),
	}

	onMaster := dial(t, masterAddr)
	do(t, onMaster, "SET", "foo", "bar")
	expectReply(t, onMaster, "+OK\r\n")

	for _, addr := range replicaAddrs {
		expectEventually(t, dial(t, addr), "$3\r\nbar\r\n", "GET", "foo")
	}
}
