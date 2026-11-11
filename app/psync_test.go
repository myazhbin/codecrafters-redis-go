package main

import (
	"net"
	"strings"
	"testing"
)

// handshakeUntilPsync plays the replica's part of the handshake on conn, up
// to but not including PSYNC.
func handshakeUntilPsync(t *testing.T, conn net.Conn) {
	t.Helper()

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "REPLCONF", "listening-port", "6380")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "REPLCONF", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
}

func TestMasterAnswersPsyncWithFullResync(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	handshakeUntilPsync(t, conn)
	do(t, conn, "PSYNC", "?", "-1")
	expectReply(t, conn, "+FULLRESYNC "+srv.replID+" 0\r\n")
}

func TestFullResyncReplyFormat(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PSYNC", "?", "-1")
	line := readReplyLine(t, conn)

	parts := strings.Split(line, " ")
	if len(parts) != 3 || parts[0] != "+FULLRESYNC" {
		t.Fatalf("reply = %q, want +FULLRESYNC <replication ID> <offset>", line)
	}
	if !replicationIDPattern.MatchString(parts[1]) {
		t.Errorf("replication ID = %q, want 40 alphanumeric characters", parts[1])
	}
	if parts[2] != "0" {
		t.Errorf("offset = %q, want 0", parts[2])
	}
}

func TestFullResyncAnnouncesTheReplicationIDOfInfo(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	replica := dial(t, addr)

	fields, _ := info(t, conn, "replication")

	do(t, replica, "PSYNC", "?", "-1")
	expectReply(t, replica, "+FULLRESYNC "+fields["master_replid"]+" "+fields["master_repl_offset"]+"\r\n")
}

func TestPsyncIsCaseInsensitive(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "psync", "?", "-1")
	expectReply(t, conn, "+FULLRESYNC "+srv.replID+" 0\r\n")
}

func TestPsyncWithAnUnknownHistoryIsAFullResync(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The replica claims to follow another master, or this one before it
	// restarted: it gets everything again.
	do(t, conn, "PSYNC", fakeReplID, "1234")
	expectReply(t, conn, "+FULLRESYNC "+srv.replID+" 0\r\n")
}

func TestPsyncErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PSYNC", "?")
	expectReply(t, conn, "-ERR wrong number of arguments for 'psync' command\r\n")
	do(t, conn, "PSYNC", "?", "-1", "extra")
	expectReply(t, conn, "-ERR wrong number of arguments for 'psync' command\r\n")
	do(t, conn, "PSYNC", "?", "abc")
	expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
}

func TestEveryReplicaGetsTheSameReplicationID(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)

	for range 3 {
		replica := dial(t, addr)
		handshakeUntilPsync(t, replica)
		do(t, replica, "PSYNC", "?", "-1")
		expectReply(t, replica, "+FULLRESYNC "+srv.replID+" 0\r\n")
	}
}
