package main

import (
	"net"
	"sort"
	"testing"
	"time"
)

// noAuthError is the reply to a command sent by a client that has to
// authenticate first.
const noAuthError = "-NOAUTH Authentication required.\r\n"

func TestAuthenticationIsRequiredOnceTheDefaultUserHasAPassword(t *testing.T) {
	addr := startTestServer(t)

	first := dial(t, addr)
	do(t, first, "ACL", "SETUSER", "default", ">newpassword")
	expectReply(t, first, "+OK\r\n")
	// The connection that was there before stays authenticated.
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, "$7\r\ndefault\r\n")

	// A new connection isn't.
	second := dial(t, addr)
	do(t, second, "ACL", "WHOAMI")
	expectReply(t, second, "-NOAUTH Authentication required.\r\n")
}

func TestConnectionsAreAuthenticatedAsLongAsTheDefaultUserHasNoPassword(t *testing.T) {
	addr := startTestServer(t)

	// Every connection is the default user, without having to do anything.
	for range 3 {
		conn := dial(t, addr)
		do(t, conn, "ACL", "WHOAMI")
		expectReply(t, conn, bulk("default"))
		do(t, conn, "PING")
		expectReply(t, conn, "+PONG\r\n")
	}

	// Another user having a password changes nothing to that.
	conn := dial(t, addr)
	setUser(t, conn, "alice", ">secret")
	late := dial(t, addr)
	do(t, late, "ACL", "WHOAMI")
	expectReply(t, late, bulk("default"))
}

func TestEveryCommandRequiresAuthentication(t *testing.T) {
	addr := startTestServer(t)
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cmd := commands[name]
		if cmd.noAuth {
			continue
		}
		t.Run(name, func(t *testing.T) {
			conn := dial(t, addr)

			// The command is given as many arguments as it takes. What they
			// are doesn't matter: it is refused before they are looked at.
			args := []string{name}
			for len(args) < max(cmd.arity, -cmd.arity) {
				args = append(args, "arg")
			}
			do(t, conn, args...)
			expectReply(t, conn, noAuthError)
			// Again and again, for as long as the client doesn't
			// authenticate.
			do(t, conn, args...)
			expectReply(t, conn, noAuthError)
		})
	}
}

func TestRefusedCommandsOfAnUnauthenticatedClientHaveNoEffect(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	admin := dial(t, addr)

	do(t, admin, "SET", "counter", "1")
	expectReply(t, admin, "+OK\r\n")
	expectPropagated(t, replica, "SET", "counter", "1")
	setUser(t, admin, "default", ">newpassword")

	stranger := dial(t, addr)
	for _, command := range [][]string{
		{"SET", "key", "value"},
		{"INCR", "counter"},
		{"RPUSH", "list_key", "a"},
		{"ZADD", "zset_key", "1", "a"},
		{"SETBIT", "bitmap_key", "1", "1"},
		{"GEOADD", "places", "2", "48", "somewhere"},
		{"SUBSCRIBE", "foo"},
		{"MULTI"},
		{"ACL", "SETUSER", "default", "nopass"},
		{"ACL", "SETUSER", "mallory", "nopass"},
		// The commands that would wait are refused right away too.
		{"BLPOP", "list_key", "0"},
		{"XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$"},
	} {
		do(t, stranger, command...)
		expectReply(t, stranger, noAuthError)
	}

	// Nothing was written, nobody is subscribed, no transaction was
	// started, and the users are as they were.
	do(t, admin, "KEYS", "*")
	expectReply(t, admin, bulkArray("counter"))
	do(t, admin, "GET", "counter")
	expectReply(t, admin, bulk("1"))
	do(t, admin, "PUBLISH", "foo", "msg")
	expectReply(t, admin, ":0\r\n")
	expectUser(t, admin, "default", nil, []string{sha256Hex("newpassword")})
	do(t, admin, "ACL", "GETUSER", "mallory")
	expectReply(t, admin, "$-1\r\n")
	if got, want := readFile(t, incrPath), encodeCommand("SET", "counter", "1"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	expectNoReply(t, stranger, 50*time.Millisecond)
}

func TestInvalidCommandsOfAnUnauthenticatedClient(t *testing.T) {
	addr := startTestServer(t)
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")

	// A command has to exist, and to be well-formed, before who sends it
	// matters.
	stranger := dial(t, addr)
	do(t, stranger, "NOSUCHCOMMAND", "arg")
	expectReply(t, stranger, "-ERR unknown command 'NOSUCHCOMMAND', with args beginning with: 'arg' \r\n")
	do(t, stranger, "GET")
	expectReply(t, stranger, "-ERR wrong number of arguments for 'get' command\r\n")
	do(t, stranger, "GET", "foo")
	expectReply(t, stranger, noAuthError)
}

func TestPipelinedCommandsOfAnUnauthenticatedClient(t *testing.T) {
	addr := startTestServer(t)
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")

	stranger := dial(t, addr)
	send(t, stranger, encodeCommand("PING")+encodeCommand("SET", "foo", "bar")+encodeCommand("GET", "foo"))
	expectReply(t, stranger, noAuthError+noAuthError+noAuthError)
}

func TestConnectionsMadeBeforeThePasswordWasSetStayAuthenticated(t *testing.T) {
	addr := startTestServer(t)

	// Some have been used, one hasn't sent anything yet.
	used, idle, admin := dial(t, addr), dial(t, addr), dial(t, addr)
	do(t, used, "SET", "foo", "bar")
	expectReply(t, used, "+OK\r\n")
	// The server has to have taken the idle connection in before the
	// password is set, for it to count as having been there.
	do(t, admin, "PING")
	expectReply(t, admin, "+PONG\r\n")
	time.Sleep(50 * time.Millisecond)

	setUser(t, admin, "default", ">newpassword")

	for name, conn := range map[string]net.Conn{"used": used, "idle": idle, "admin": admin} {
		do(t, conn, "ACL", "WHOAMI")
		expectReply(t, conn, bulk("default"))
		do(t, conn, "GET", "foo")
		expectReply(t, conn, bulk("bar"))
		do(t, conn, "INCR", "counter-of-"+name)
		expectReply(t, conn, ":1\r\n")
	}

	// They still are after the password changes again.
	setUser(t, admin, "default", ">another", "<newpassword")
	do(t, used, "GET", "foo")
	expectReply(t, used, bulk("bar"))
}

func TestAuthenticationIsNotRequiredAnymoreOnceThePasswordIsGone(t *testing.T) {
	addr := startTestServer(t)
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")

	stranger := dial(t, addr)
	do(t, stranger, "PING")
	expectReply(t, stranger, noAuthError)

	// With the nopass flag back, nobody has to authenticate: neither the new
	// connections nor the ones that were kept out.
	setUser(t, admin, "default", "nopass")
	do(t, stranger, "PING")
	expectReply(t, stranger, "+PONG\r\n")
	do(t, stranger, "ACL", "WHOAMI")
	expectReply(t, stranger, bulk("default"))
	late := dial(t, addr)
	do(t, late, "PING")
	expectReply(t, late, "+PONG\r\n")

	// And they have to again when a password is set again, unless they
	// connected in between.
	setUser(t, admin, "default", ">other")
	do(t, stranger, "PING")
	expectReply(t, stranger, noAuthError)
	do(t, late, "PING")
	expectReply(t, late, "+PONG\r\n")
}

func TestDefaultUserWithoutPasswordNorNopassKeepsNewConnectionsOut(t *testing.T) {
	addr := startTestServer(t)
	admin := dial(t, addr)

	// No password will do for the default user: new connections can't be
	// let in as that user.
	setUser(t, admin, "default", "resetpass")
	stranger := dial(t, addr)
	do(t, stranger, "PING")
	expectReply(t, stranger, noAuthError)
	do(t, admin, "PING")
	expectReply(t, admin, "+PONG\r\n")
}

func TestReplicaAppliesItsMastersCommandsWhateverItsUsers(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	// The clients of the replica have to authenticate from now on.
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")
	stranger := dial(t, addr)
	do(t, stranger, "GET", "foo")
	expectReply(t, stranger, noAuthError)

	// The master isn't one of them: what it sends is applied.
	link.propagate([]string{"SET", "foo", "1"}, []string{"INCR", "foo"})
	expectEventually(t, admin, "$1\r\n2\r\n", "GET", "foo")
	// SET foo 1 is 29 bytes long, and INCR foo 23.
	link.propagate([]string{"REPLCONF", "GETACK", "*"})
	link.expectCommand("REPLCONF", "ACK", "52")
}

func TestReplicasKeepReceivingWritesOnceTheMasterHasAPassword(t *testing.T) {
	addr := startTestServer(t)
	replica := connectReplica(t, addr)
	admin := dial(t, addr)

	setUser(t, admin, "default", ">newpassword")
	do(t, admin, "SET", "foo", "bar")
	expectReply(t, admin, "+OK\r\n")
	// The replica connected before: it is still served.
	expectPropagated(t, replica, "SET", "foo", "bar")
}

func TestAppendOnlyFileIsReplayedWhateverItHolds(t *testing.T) {
	// Users aren't logged. A file that sets a password anyway is replayed to
	// its end: the commands of the file aren't those of a client that has to
	// authenticate.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "before", "1"},
		[]string{"ACL", "SETUSER", "default", ">newpassword"},
		[]string{"SET", "after", "2"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	addr := startServer(t, srv)

	srv.mu.Lock()
	_, after := srv.store.entries["after"]
	srv.mu.Unlock()
	if !after {
		t.Errorf("the command that follows ACL SETUSER in the append-only file wasn't replayed")
	}
	// The password was set, though: clients have to authenticate.
	conn := dial(t, addr)
	do(t, conn, "GET", "after")
	expectReply(t, conn, noAuthError)
}
