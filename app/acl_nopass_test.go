package main

import (
	"reflect"
	"testing"
)

func TestDefaultUserHasTheNopassFlag(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The flags are an array of bulk strings.
	do(t, conn, "ACL", "GETUSER", "default")
	if got := readReplyLine(t, conn); got != "*2" && got != "*4" {
		t.Fatalf("reply starts with %q, want an array of one or two properties and their values", got)
	}
	expectReply(t, conn, "$5\r\nflags\r\n*1\r\n$6\r\nnopass\r\n")
	drainPendingReply(t, conn)

	if got, want := userFlags(t, conn, "default"), []string{"nopass"}; !reflect.DeepEqual(got, want) {
		t.Errorf("flags of the default user = %q, want %q", got, want)
	}
}

func TestNopassFlagIsThereForEveryConnection(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)

	// The flag is a property of the user, not of the connection that asks.
	for range 3 {
		conn := dial(t, addr)
		if got, want := userFlags(t, conn, "default"), []string{"nopass"}; !reflect.DeepEqual(got, want) {
			t.Errorf("flags of the default user = %q, want %q", got, want)
		}
		// Which is why the connection is the default user without having
		// had to give a password.
		do(t, conn, "ACL", "WHOAMI")
		expectReply(t, conn, bulk("default"))
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.defaultUser.nopass || srv.users["default"] != srv.defaultUser {
		t.Errorf("the default user of the server isn't the user named default, without password")
	}
}

func TestNopassFlagInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	if got := readReplyLine(t, conn); got != "*1" {
		t.Fatalf("reply to EXEC starts with %q, want *1", got)
	}
	if got := readReplyLine(t, conn); got != "*2" && got != "*4" {
		t.Fatalf("reply to ACL GETUSER starts with %q, want an array of one or two properties and their values", got)
	}
	expectReply(t, conn, "$5\r\nflags\r\n*1\r\n$6\r\nnopass\r\n")
	drainPendingReply(t, conn)
}
