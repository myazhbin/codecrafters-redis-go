package main

import (
	"strings"
	"testing"
	"time"
)

// fakeReplID is the replication ID fake masters introduce themselves with.
const fakeReplID = "75cd7bc10c49047e0d163660f3b90625b1af31dc"

// acceptHandshakeUntilPsync accepts the replica's connection and plays the
// master's part of the handshake up to, and including, receiving PSYNC. The
// reply to PSYNC is left to the caller.
func (m *fakeMaster) acceptHandshakeUntilPsync() *masterSide {
	m.t.Helper()

	link := m.accept()
	link.expectCommand("PING")
	link.reply("+PONG\r\n")
	if got := link.readCommand(); len(got) != 3 || !strings.EqualFold(got[0], "REPLCONF") || got[1] != "listening-port" {
		m.t.Fatalf("replica sent %q, want REPLCONF listening-port <port>", got)
	}
	link.reply("+OK\r\n")
	link.expectCommand("REPLCONF", "capa", "psync2")
	link.reply("+OK\r\n")
	link.expectCommand("PSYNC", "?", "-1")
	return link
}

func TestReplicaSendsPsyncAfterReplconf(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.accept()
	link.expectCommand("PING")
	link.reply("+PONG\r\n")
	link.expectCommand("REPLCONF", "listening-port", "6379")
	link.reply("+OK\r\n")
	link.expectCommand("REPLCONF", "capa", "psync2")
	// PSYNC only comes once the second REPLCONF has been answered.
	link.expectSilence(100 * time.Millisecond)
	link.reply("+OK\r\n")
	link.expectCommand("PSYNC", "?", "-1")
}

func TestReplicaSendsPsyncWithTheExactEncoding(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.accept()
	for _, reply := range []string{"+PONG\r\n", "+OK\r\n", "+OK\r\n"} {
		link.readCommand()
		link.reply(reply)
	}

	want := "*3\r\n$5\r\nPSYNC\r\n$1\r\n?\r\n$2\r\n-1\r\n"
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

func TestReplicaStaysConnectedAfterFullResync(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)

	link := master.acceptHandshakeUntilPsync()
	link.reply("+FULLRESYNC " + fakeReplID + " 0\r\n")

	// The handshake is over: the replica has nothing more to say, keeps the
	// connection open and goes on serving its clients.
	link.expectSilence(100 * time.Millisecond)
	conn := dial(t, addr)
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestHandshakeFailsOnUnexpectedPsyncReply(t *testing.T) {
	for _, reply := range []string{
		"-ERR unknown command 'PSYNC'\r\n",
		"+OK\r\n",
		"+FULLRESYNC\r\n",
		"+FULLRESYNC " + fakeReplID + "\r\n",
		"+FULLRESYNC " + fakeReplID + " abc\r\n",
		"+FULLRESYNC " + fakeReplID + " -1\r\n",
	} {
		t.Run(strings.TrimSpace(reply), func(t *testing.T) {
			master := startFakeMaster(t)
			cfg := defaultConfig()
			cfg.replicaOf = master.address()

			done := make(chan error, 1)
			go func() { done <- NewServerWithConfig(cfg).followMaster() }()

			link := master.acceptHandshakeUntilPsync()
			link.reply(reply)

			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "PSYNC: unexpected reply") {
					t.Errorf("followMaster returned %v, want an error about the reply to PSYNC", err)
				}
			case <-time.After(replyTimeout):
				t.Fatalf("followMaster didn't give up")
			}
		})
	}
}

func TestParseFullResync(t *testing.T) {
	replID, offset, err := parseFullResync("+FULLRESYNC " + fakeReplID + " 0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if replID != fakeReplID || offset != 0 {
		t.Errorf("got (%q, %d), want (%q, 0)", replID, offset, fakeReplID)
	}

	if _, offset, err = parseFullResync("+FULLRESYNC " + fakeReplID + " 1234"); err != nil || offset != 1234 {
		t.Errorf("offset = %d, err = %v, want 1234 and no error", offset, err)
	}
}
