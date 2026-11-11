package main

import "testing"

func TestMasterAnswersTheStartOfTheHandshake(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "REPLCONF", "listening-port", "6380")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "REPLCONF", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
}

func TestReplconfIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "replconf", "LISTENING-PORT", "6380")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ReplConf", "CAPA", "psync2")
	expectReply(t, conn, "+OK\r\n")
}

func TestReplconfSeveralOptionsAtOnce(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Real replicas send several capabilities in a single command.
	do(t, conn, "REPLCONF", "capa", "eof", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "REPLCONF", "listening-port", "6380", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
}

func TestReplconfUnknownCapabilityIsIgnored(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "REPLCONF", "capa", "something-new")
	expectReply(t, conn, "+OK\r\n")
}

func TestReplconfErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"REPLCONF", "capa"}, "-ERR syntax error\r\n"},
		{[]string{"REPLCONF", "listening-port", "6380", "capa"}, "-ERR syntax error\r\n"},
		{[]string{"REPLCONF", "nope", "1"}, "-ERR Unrecognized REPLCONF option: nope\r\n"},
		{[]string{"REPLCONF", "listening-port", "abc"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"REPLCONF", "listening-port", "70000"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"REPLCONF", "listening-port", "-1"}, "-ERR value is not an integer or out of range\r\n"},
	}
	for _, tt := range tests {
		do(t, conn, tt.args...)
		expectReply(t, conn, tt.want)
	}

	// The connection is still usable after errors.
	do(t, conn, "REPLCONF", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
}

func TestReplconfRemembersTheListeningPort(t *testing.T) {
	srv := NewServer()
	c := &client{server: srv}

	if got := string(c.execute([]string{"REPLCONF", "listening-port", "6380"})); got != "+OK\r\n" {
		t.Fatalf("reply = %q, want +OK", got)
	}
	if c.listeningPort != 6380 {
		t.Errorf("listening port = %d, want 6380", c.listeningPort)
	}

	// A command that is rejected changes nothing, even if it starts with a
	// valid option.
	c.execute([]string{"REPLCONF", "listening-port", "7000", "nope", "1"})
	if c.listeningPort != 6380 {
		t.Errorf("listening port = %d after a rejected REPLCONF, want 6380", c.listeningPort)
	}
}

func TestReplconfDoesNotDisturbRegularCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "REPLCONF", "listening-port", "6380")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
}
