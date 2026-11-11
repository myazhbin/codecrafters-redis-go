package main

import (
	"testing"
	"time"
)

func TestACLWhoAmI(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The name of the user, as a bulk string.
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, "$7\r\ndefault\r\n")
}

func TestEveryConnectionIsTheDefaultUser(t *testing.T) {
	addr := startTestServer(t)

	first, second := dial(t, addr), dial(t, addr)
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, bulk("default"))
	do(t, second, "ACL", "WHOAMI")
	expectReply(t, second, bulk("default"))

	// Asking again changes nothing, and neither does using the connection
	// for something else in between.
	do(t, first, "SET", "foo", "bar")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, bulk("default"))

	// A connection that comes later is the default user too.
	third := dial(t, addr)
	do(t, third, "ACL", "WHOAMI")
	expectReply(t, third, bulk("default"))
}

func TestACLNamesAreCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, names := range [][2]string{{"ACL", "WHOAMI"}, {"acl", "whoami"}, {"Acl", "WhoAmI"}, {"aCL", "Whoami"}} {
		do(t, conn, names[0], names[1])
		expectReply(t, conn, bulk("default"))
	}
}

func TestACLInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ACL")
	expectReply(t, conn, "-ERR wrong number of arguments for 'acl' command\r\n")
	do(t, conn, "ACL", "WHOAMI", "extra")
	expectReply(t, conn, "-ERR wrong number of arguments for 'acl|whoami' command\r\n")

	for _, subcommand := range []string{"WHOIS", "WHO", "", "WHOAMI ", "ME"} {
		do(t, conn, "ACL", subcommand)
		expectReply(t, conn, "-ERR unknown subcommand '"+subcommand+"'. Try ACL HELP.\r\n")
	}

	// The connection is still usable.
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))
}

func TestACLWhoAmIInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "ACL", "WHOAREYOU")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(bulk("default"), "-ERR unknown subcommand 'WHOAREYOU'. Try ACL HELP.\r\n"))
}

func TestACLWhoAmIInSubscribedMode(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	// ACL isn't one of the commands of subscribed mode. It is named along
	// with its subcommand.
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, notAllowedInSubscribedMode("acl|whoami"))
}

func TestACLWhoAmIIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))

	// Nothing is logged or propagated, and no key is created.
	if got := readFile(t, incrPath); got != "" {
		t.Errorf("append-only file holds %q, want nothing", got)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
	// A client that is a replica is a client too.
	do(t, replica, "ACL", "WHOAMI")
	expectReply(t, replica, bulk("default"))
}

func TestACLWhoAmIFromTheAppendOnlyFileAndFromTheMaster(t *testing.T) {
	// The command is never logged or propagated. If it is found in a file or
	// in the replication stream anyway, it is executed without harm.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"ACL", "WHOAMI"},
		[]string{"SET", "after", "1"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))
	do(t, conn, "GET", "after")
	expectReply(t, conn, bulk("1"))

	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()
	link.propagate([]string{"ACL", "WHOAMI"}, []string{"SET", "foo", "1"})
	conn = dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	link.expectSilence(50 * time.Millisecond)
}
