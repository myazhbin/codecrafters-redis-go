package main

import (
	"testing"
	"time"
)

func TestReplicaAnswersGetAckWithAck(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"REPLCONF", "GETACK", "*"})
	link.expectCommand("REPLCONF", "ACK", "0")
}

func TestReplicaSendsAckWithTheExactEncoding(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	// Real masters send the command in lower case.
	link.reply("*3\r\n$8\r\nreplconf\r\n$6\r\ngetack\r\n$1\r\n*\r\n")

	want := "*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$1\r\n0\r\n"
	got := make([]byte, len(want))
	link.conn.SetReadDeadline(time.Now().Add(replyTimeout))
	for n := 0; n < len(got); {
		m, err := link.r.Read(got[n:])
		if err != nil {
			t.Fatalf("reading from the replica: %v (got %q so far)", err, got[:n])
		}
		n += m
	}
	if string(got) != want {
		t.Errorf("replica sent %q, want %q", got, want)
	}
}

func TestReplicaAnswersGetAckSentAlongWithTheHandshakeReply(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.acceptHandshakeUntilPsync()
	link.reply(fullResync() + encodeCommand("REPLCONF", "GETACK", "*"))
	link.expectCommand("REPLCONF", "ACK", "0")
}

func TestReplicaOnlyRepliesToGetAck(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate(
		[]string{"PING"},
		[]string{"SET", "foo", "1"},
		[]string{"REPLCONF", "capa", "psync2"},
	)
	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	link.expectSilence(100 * time.Millisecond)

	// The first thing the master hears is the answer to GETACK.
	link.propagate([]string{"REPLCONF", "GETACK", "*"})
	if got := link.readCommand(); len(got) != 3 || got[0] != "REPLCONF" || got[1] != "ACK" {
		t.Errorf("replica sent %q, want REPLCONF ACK <offset>", got)
	}
	link.expectSilence(50 * time.Millisecond)
}

func TestGetAckFromARegularClientIsIgnored(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	// Only the master gets acknowledgements, and it gets them on its own
	// connection.
	conn := dial(t, addr)
	do(t, conn, "REPLCONF", "GETACK", "*")
	expectNoReply(t, conn, 50*time.Millisecond)
	link.expectSilence(50 * time.Millisecond)

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestGetAckOnAMasterIsIgnored(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "REPLCONF", "GETACK", "*")
	expectNoReply(t, conn, 50*time.Millisecond)
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestGetAckErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "REPLCONF", "GETACK")
	expectReply(t, conn, "-ERR syntax error\r\n")

}

func TestReplconfIsNotQueuedInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "REPLCONF", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, "*0\r\n")
}

func TestReplicaAcknowledgesInTheMiddleOfATransaction(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"MULTI"}, []string{"SET", "foo", "1"})
	link.propagate([]string{"REPLCONF", "GETACK", "*"})
	// 15 bytes for MULTI and 29 for SET.
	link.expectCommand("REPLCONF", "ACK", "44")

	link.propagate([]string{"EXEC"})
	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	link.expectSilence(50 * time.Millisecond)
}

func TestIsGetAck(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"REPLCONF", "GETACK", "*"}, true},
		{[]string{"replconf", "getack", "*"}, true},
		{[]string{"REPLCONF", "ACK", "0"}, false},
		{[]string{"REPLCONF", "capa", "psync2"}, false},
		{[]string{"REPLCONF"}, false},
		{[]string{"GETACK", "*"}, false},
	}
	for _, tt := range tests {
		if got := isGetAck(tt.args); got != tt.want {
			t.Errorf("isGetAck(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
