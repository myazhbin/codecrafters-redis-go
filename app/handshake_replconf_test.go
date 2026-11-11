package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReplicaSendsReplconfAfterPing(t *testing.T) {
	master := startFakeMaster(t)
	srv, _ := startReplicaOf(t, master)

	link := master.accept()
	link.expectCommand("PING")
	link.reply("+PONG\r\n")
	link.expectCommand("REPLCONF", "listening-port", strconv.Itoa(srv.cfg.port))
	link.reply("+OK\r\n")
	link.expectCommand("REPLCONF", "capa", "psync2")
}

func TestReplicaAnnouncesItsOwnListeningPort(t *testing.T) {
	master := startFakeMaster(t)

	cfg, err := parseConfig([]string{"--port", "6380"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	cfg.replicaOf = master.address()
	srv := NewServerWithConfig(cfg)

	done := make(chan error, 1)
	go func() { done <- srv.followMaster() }()

	link := master.accept()
	link.expectCommand("PING")
	link.reply("+PONG\r\n")
	// The port announced is the one given with --port, not the master's.
	link.expectCommand("REPLCONF", "listening-port", "6380")
	link.reply("+OK\r\n")
	link.expectCommand("REPLCONF", "capa", "psync2")

	link.conn.Close()
	select {
	case <-done:
	case <-time.After(replyTimeout):
		t.Fatalf("followMaster didn't return after the master closed the connection")
	}
}

func TestReplicaSendsReplconfWithTheExactEncoding(t *testing.T) {
	master := startFakeMaster(t)

	cfg := defaultConfig()
	cfg.port = 6380
	cfg.replicaOf = master.address()
	go NewServerWithConfig(cfg).followMaster()

	link := master.accept()
	link.expectCommand("PING")
	link.reply("+PONG\r\n")

	for _, want := range []string{
		"*3\r\n$8\r\nREPLCONF\r\n$14\r\nlistening-port\r\n$4\r\n6380\r\n",
		"*3\r\n$8\r\nREPLCONF\r\n$4\r\ncapa\r\n$6\r\npsync2\r\n",
	} {
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
			t.Fatalf("replica sent %q, want %q", got, want)
		}
		link.reply("+OK\r\n")
	}
}

func TestReplicaWaitsForEachReplyBeforeTheNextStep(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.accept()
	link.expectCommand("PING")
	// Nothing more comes as long as the master hasn't answered.
	link.expectSilence(100 * time.Millisecond)
	link.reply("+PONG\r\n")

	link.expectCommand("REPLCONF", "listening-port", "6379")
	link.expectSilence(100 * time.Millisecond)
	link.reply("+OK\r\n")

	link.expectCommand("REPLCONF", "capa", "psync2")
}

func TestHandshakeStopsOnUnexpectedReply(t *testing.T) {
	tests := []struct {
		name string
		// replies are what the master answers, in order.
		replies []string
		want    string // a fragment of the error
	}{
		{"error to PING", []string{"-ERR nope\r\n"}, "PING: unexpected reply"},
		{"wrong reply to PING", []string{"+OK\r\n"}, "PING: unexpected reply"},
		{"error to listening-port", []string{"+PONG\r\n", "-ERR nope\r\n"}, "REPLCONF listening-port 6379: unexpected reply"},
		{"error to capa", []string{"+PONG\r\n", "+OK\r\n", "-ERR nope\r\n"}, "REPLCONF capa psync2: unexpected reply"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			master := startFakeMaster(t)
			cfg := defaultConfig()
			cfg.replicaOf = master.address()

			done := make(chan error, 1)
			go func() { done <- NewServerWithConfig(cfg).followMaster() }()

			link := master.accept()
			for _, reply := range tt.replies {
				link.readCommand()
				link.reply(reply)
			}

			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Errorf("followMaster returned %v, want an error mentioning %q", err, tt.want)
				}
			case <-time.After(replyTimeout):
				t.Fatalf("followMaster didn't give up")
			}
			// The handshake was abandoned: nothing else was sent.
			link.expectSilence(50 * time.Millisecond)
		})
	}
}

func TestHandshakeFailsWhenTheMasterHangsUp(t *testing.T) {
	master := startFakeMaster(t)
	cfg := defaultConfig()
	cfg.replicaOf = master.address()

	done := make(chan error, 1)
	go func() { done <- NewServerWithConfig(cfg).followMaster() }()

	link := master.accept()
	link.expectCommand("PING")
	link.conn.Close()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "handshake") {
			t.Errorf("followMaster returned %v, want a handshake error", err)
		}
	case <-time.After(replyTimeout):
		t.Fatalf("followMaster didn't return after the master closed the connection")
	}
}
