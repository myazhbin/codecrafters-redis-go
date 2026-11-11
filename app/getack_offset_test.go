package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// expectAck asks the replica for an acknowledgement and checks the offset it
// answers with.
func (m *masterSide) expectAck(offset int) {
	m.t.Helper()

	m.propagate([]string{"REPLCONF", "GETACK", "*"})
	m.expectCommand("REPLCONF", "ACK", strconv.Itoa(offset))
}

func TestAckOffsetCountsTheBytesOfProcessedCommands(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	link.expectAck(0)
	link.propagate([]string{"PING"})
	// 37 bytes for the first GETACK, 14 for PING.
	link.expectAck(51)
	link.propagate([]string{"SET", "foo", "1"})
	link.propagate([]string{"SET", "bar", "2"})
	// 51, plus 37 for the second GETACK and 29 for each SET.
	link.expectAck(146)
}

func TestAckOffsetDoesNotIncludeTheCurrentGetAck(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	// Each GETACK is 37 bytes long, and only counts for the next one.
	link.expectAck(0)
	link.expectAck(37)
	link.expectAck(74)
	link.propagate([]string{"PING"})
	link.expectAck(125)
}

func TestAckOffsetWhenCommandsArriveInOneSegment(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	// Everything arrives at once, from the reply to PSYNC to the last
	// GETACK: the handshake and the RDB file don't count, and each offset
	// only covers what precedes its GETACK.
	link := master.acceptHandshakeUntilPsync()
	getAck := encodeCommand("REPLCONF", "GETACK", "*")
	link.reply(fullResync() +
		getAck +
		encodeCommand("PING") +
		getAck +
		encodeCommand("SET", "foo", "1") +
		encodeCommand("SET", "bar", "2") +
		getAck)

	link.expectCommand("REPLCONF", "ACK", "0")
	link.expectCommand("REPLCONF", "ACK", "51")
	link.expectCommand("REPLCONF", "ACK", "146")
}

func TestAckOffsetWhenCommandsAreSplitAcrossSegments(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	stream := encodeCommand("SET", "foo", "123") + encodeCommand("REPLCONF", "GETACK", "*")
	for _, chunk := range []string{stream[:5], stream[5:18], stream[18:40], stream[40:]} {
		link.reply(chunk)
		time.Sleep(5 * time.Millisecond)
	}
	link.expectCommand("REPLCONF", "ACK", "31")
}

func TestAckOffsetCountsEveryKindOfCommand(t *testing.T) {
	commands := [][]string{
		{"PING"},
		{"SET", "foo", "1"},
		{"set", "foo", "2"},                        // letter case doesn't matter
		{"INCR", "text"},                           // fails on the replica
		{"NOSUCHCOMMAND", "arg"},                   // unknown
		{"SET", "foo"},                             // wrong number of arguments
		{"REPLCONF", "capa", "psync2"},             // not a write
		{"SET", "big", strings.Repeat("x", 10000)}, // larger than a line
		{"MULTI"}, {"SET", "foo", "3"}, {"EXEC"},   // a transaction
		{"XADD", "stream_key", "1-1", "field", "value"},
	}

	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"SET", "text", "abc"})
	offset := len(encodeCommand("SET", "text", "abc"))
	for _, command := range commands {
		link.propagate(command)
		offset += len(encodeCommand(command...))
		link.expectAck(offset)
		offset += len(encodeCommand("REPLCONF", "GETACK", "*"))
	}
}

func TestAckOffsetAcceptsAnyGetAckArgument(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"replconf", "getack", "*"})
	link.expectCommand("REPLCONF", "ACK", "0")
	link.propagate([]string{"REPLCONF", "GETACK", "whatever"})
	link.expectCommand("REPLCONF", "ACK", "37")
	link.expectAck(37 + len(encodeCommand("REPLCONF", "GETACK", "whatever")))
}

func TestReplicaReportsItsOffsetInInfo(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()
	conn := dial(t, addr)

	fields, _ := info(t, conn, "replication")
	if got := fields["master_repl_offset"]; got != "0" {
		t.Errorf("master_repl_offset = %q before any command, want 0", got)
	}

	link.propagate([]string{"PING"}, []string{"SET", "foo", "1"})
	// Once the replica has acknowledged, the offset is known to be 43.
	link.expectAck(43)

	fields, _ = info(t, conn, "replication")
	if got, want := fields["master_repl_offset"], strconv.Itoa(43+37); got != want {
		t.Errorf("master_repl_offset = %q, want %s", got, want)
	}
}

func TestCountingReader(t *testing.T) {
	r := &countingReader{r: strings.NewReader("hello world")}

	buf := make([]byte, 5)
	if n, err := r.Read(buf); n != 5 || err != nil {
		t.Fatalf("Read = (%d, %v), want (5, nil)", n, err)
	}
	if r.n != 5 {
		t.Errorf("counted %d bytes, want 5", r.n)
	}
	buf = make([]byte, 100)
	if n, _ := r.Read(buf); n != 6 {
		t.Fatalf("Read = %d bytes, want 6", n)
	}
	if r.n != 11 {
		t.Errorf("counted %d bytes, want 11", r.n)
	}
	if n, err := r.Read(buf); n != 0 || err == nil {
		t.Errorf("Read at the end = (%d, %v), want (0, EOF)", n, err)
	}
	if r.n != 11 {
		t.Errorf("counted %d bytes after EOF, want 11", r.n)
	}
}
