package main

import "testing"

func TestWaitWithoutReplicas(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "WAIT", "0", "60000")
	expectReply(t, conn, ":0\r\n")
}

func TestWaitWithoutReplicasRepliesImmediately(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The timeout is a minute, and the test's reply timeout a couple of
	// seconds: the reply doesn't wait for the former.
	do(t, conn, "wait", "0", "60000")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "WAIT", "0", "0")
	expectReply(t, conn, ":0\r\n")
}

func TestWaitWithoutReplicasAfterWrites(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "WAIT", "0", "60000")
	expectReply(t, conn, ":0\r\n")
}

func TestWaitErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		args []string
		want string
	}{
		{[]string{"WAIT"}, "-ERR wrong number of arguments for 'wait' command\r\n"},
		{[]string{"WAIT", "0"}, "-ERR wrong number of arguments for 'wait' command\r\n"},
		{[]string{"WAIT", "0", "100", "extra"}, "-ERR wrong number of arguments for 'wait' command\r\n"},
		{[]string{"WAIT", "abc", "100"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"WAIT", "0", "abc"}, "-ERR timeout is not an integer or out of range\r\n"},
		{[]string{"WAIT", "0", "1.5"}, "-ERR timeout is not an integer or out of range\r\n"},
		{[]string{"WAIT", "0", "-1"}, "-ERR timeout is negative\r\n"},
	}
	for _, tt := range tests {
		do(t, conn, tt.args...)
		expectReply(t, conn, tt.want)
	}
}

func TestWaitIsNotPropagated(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "WAIT", "0", "100")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")
	expectPropagated(t, replica, "SET", "foo", "1")
}
