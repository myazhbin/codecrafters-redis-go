package main

import (
	"net"
	"testing"
	"time"
)

// startServerWithPassword runs a server whose default user has the given
// password, and returns its address along with a connection that was made
// before the password was set: one that doesn't have to authenticate.
func startServerWithPassword(t *testing.T, password string) (addr string, admin net.Conn) {
	t.Helper()

	addr = startTestServer(t)
	admin = dial(t, addr)
	setUser(t, admin, "default", ">"+password)
	return addr, admin
}

func TestAuthAuthenticatesTheConnection(t *testing.T) {
	addr := startTestServer(t)

	first := dial(t, addr)
	do(t, first, "ACL", "SETUSER", "default", ">newpassword")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, "$7\r\ndefault\r\n")

	// A new connection has to authenticate before it can do anything else.
	second := dial(t, addr)
	do(t, second, "PING")
	expectReply(t, second, "-NOAUTH Authentication required.\r\n")
	do(t, second, "AUTH", "default", "newpassword")
	expectReply(t, second, "+OK\r\n")
	do(t, second, "PING")
	expectReply(t, second, "+PONG\r\n")
}

func TestAuthenticatedConnectionIsTheUserItAuthenticatedAs(t *testing.T) {
	addr, _ := startServerWithPassword(t, "newpassword")

	conn := dial(t, addr)
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, noAuthError)
	auth(t, conn, true, "default", "newpassword")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))
}

func TestAuthenticationLastsForTheWholeConnection(t *testing.T) {
	addr, _ := startServerWithPassword(t, "newpassword")

	conn := dial(t, addr)
	auth(t, conn, true, "default", "newpassword")

	// There is no authenticating again: every command goes through, of
	// every kind.
	for _, tt := range []struct {
		command []string
		want    string
	}{
		{[]string{"PING"}, "+PONG\r\n"},
		{[]string{"ECHO", "hey"}, bulk("hey")},
		{[]string{"SET", "foo", "bar"}, "+OK\r\n"},
		{[]string{"GET", "foo"}, bulk("bar")},
		{[]string{"INCR", "counter"}, ":1\r\n"},
		{[]string{"RPUSH", "list_key", "a", "b"}, ":2\r\n"},
		{[]string{"LRANGE", "list_key", "0", "-1"}, bulkArray("a", "b")},
		{[]string{"XADD", "stream_key", "1-1", "k", "v"}, bulk("1-1")},
		{[]string{"ZADD", "zset_key", "1", "a"}, ":1\r\n"},
		{[]string{"ZRANGE", "zset_key", "0", "-1"}, bulkArray("a")},
		{[]string{"SETBIT", "bitmap_key", "1", "1"}, ":0\r\n"},
		{[]string{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"}, ":1\r\n"},
		{[]string{"KEYS", "*"}, bulkArray("bitmap_key", "counter", "foo", "list_key", "places", "stream_key", "zset_key")},
		{[]string{"MULTI"}, "+OK\r\n"},
		{[]string{"INCR", "counter"}, "+QUEUED\r\n"},
		{[]string{"EXEC"}, array(":2\r\n")},
		{[]string{"ACL", "WHOAMI"}, bulk("default")},
		{[]string{"PING"}, "+PONG\r\n"},
	} {
		do(t, conn, tt.command...)
		expectReply(t, conn, tt.want)
	}
}

func TestWrongPasswordLeavesTheConnectionUnauthenticated(t *testing.T) {
	addr, _ := startServerWithPassword(t, "newpassword")

	conn := dial(t, addr)
	for _, attempt := range [][]string{
		{"default", "wrongpassword"},
		{"default", ""},
		{"default", "NEWPASSWORD"},
		{"alice", "newpassword"},
		{"wrongpassword"},
	} {
		auth(t, conn, false, attempt...)
		do(t, conn, "PING")
		expectReply(t, conn, noAuthError)
	}

	// It can try again for as long as it wants.
	auth(t, conn, true, "default", "newpassword")
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestAuthWithThePasswordAloneAuthenticatesTheConnection(t *testing.T) {
	addr, _ := startServerWithPassword(t, "newpassword")

	conn := dial(t, addr)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, noAuthError)
	do(t, conn, "AUTH", "newpassword")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))
}

func TestEachConnectionAuthenticatesForItself(t *testing.T) {
	addr, admin := startServerWithPassword(t, "newpassword")

	first, second := dial(t, addr), dial(t, addr)
	auth(t, first, true, "default", "newpassword")

	// The other connection is still kept out, and so is one that comes
	// later.
	do(t, second, "PING")
	expectReply(t, second, noAuthError)
	third := dial(t, addr)
	do(t, third, "PING")
	expectReply(t, third, noAuthError)

	do(t, first, "SET", "foo", "bar")
	expectReply(t, first, "+OK\r\n")
	do(t, admin, "GET", "foo")
	expectReply(t, admin, bulk("bar"))

	// A connection that is closed takes its authentication with it.
	first.Close()
	again := dial(t, addr)
	do(t, again, "GET", "foo")
	expectReply(t, again, noAuthError)
	auth(t, again, true, "default", "newpassword")
	do(t, again, "GET", "foo")
	expectReply(t, again, bulk("bar"))
}

func TestAuthenticatingAsAnotherUserThanTheDefaultOne(t *testing.T) {
	addr, admin := startServerWithPassword(t, "newpassword")
	setUser(t, admin, "alice", ">secret")
	setUser(t, admin, "guest", "nopass")

	// Any user will do to be let in.
	conn := dial(t, addr)
	auth(t, conn, false, "alice", "newpassword")
	do(t, conn, "PING")
	expectReply(t, conn, noAuthError)
	auth(t, conn, true, "alice", "secret")
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("alice"))

	// A user without password takes any, but it still takes authenticating
	// as that user.
	other := dial(t, addr)
	do(t, other, "ACL", "WHOAMI")
	expectReply(t, other, noAuthError)
	auth(t, other, true, "guest", "anything")
	do(t, other, "ACL", "WHOAMI")
	expectReply(t, other, bulk("guest"))
}

func TestAuthenticatedConnectionStaysAuthenticated(t *testing.T) {
	addr, admin := startServerWithPassword(t, "newpassword")

	conn := dial(t, addr)
	auth(t, conn, true, "default", "newpassword")

	// A failed attempt to authenticate again changes nothing.
	auth(t, conn, false, "default", "wrongpassword")
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")

	// Neither does the password being changed or removed afterwards: it was
	// the right one when it was given.
	setUser(t, admin, "default", ">another", "<newpassword")
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	setUser(t, admin, "default", "resetpass")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))

	// The old password doesn't let anyone else in, though.
	stranger := dial(t, addr)
	auth(t, stranger, false, "default", "newpassword")
	do(t, stranger, "PING")
	expectReply(t, stranger, noAuthError)
}

func TestPipelinedAuthentication(t *testing.T) {
	addr, _ := startServerWithPassword(t, "newpassword")

	// The commands arrive in a single write: each of them is answered
	// according to what the connection is when its turn comes.
	conn := dial(t, addr)
	send(t, conn, encodeCommand("PING")+encodeCommand("AUTH", "default", "wrong")+encodeCommand("PING")+
		encodeCommand("AUTH", "default", "newpassword")+encodeCommand("PING")+encodeCommand("ECHO", "in"))
	expectReply(t, conn, noAuthError+wrongPassError+noAuthError+"+OK\r\n"+"+PONG\r\n"+bulk("in"))
}

func TestAuthenticatedClientCanSubscribeAndBlock(t *testing.T) {
	addr, admin := startServerWithPassword(t, "newpassword")

	// A subscriber, once it is let in, gets what is published.
	subscriber := dial(t, addr)
	do(t, subscriber, "SUBSCRIBE", "news")
	expectReply(t, subscriber, noAuthError)
	auth(t, subscriber, true, "default", "newpassword")
	do(t, subscriber, "SUBSCRIBE", "news")
	expectReply(t, subscriber, subscribed("news", 1))
	do(t, admin, "PUBLISH", "news", "hello")
	expectReply(t, admin, ":1\r\n")
	expectReply(t, subscriber, message("news", "hello"))

	// And a client that waits for an element is served.
	waiter := dial(t, addr)
	auth(t, waiter, true, "default", "newpassword")
	do(t, waiter, "BLPOP", "queue", "0")
	expectNoReply(t, waiter, 50*time.Millisecond)
	do(t, admin, "RPUSH", "queue", "job")
	expectReply(t, admin, ":1\r\n")
	expectReply(t, waiter, bulkArray("queue", "job"))
}

func TestWritesOfAnAuthenticatedClientAreLoggedAndPropagated(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	admin := dial(t, addr)
	setUser(t, admin, "default", ">newpassword")

	conn := dial(t, addr)
	do(t, conn, "SET", "foo", "refused")
	expectReply(t, conn, noAuthError)
	auth(t, conn, true, "default", "newpassword")
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")

	// Only the write that was accepted, and nothing of the authentication.
	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "bar"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectPropagated(t, replica, "SET", "foo", "bar")
	expectNoReply(t, replica, 50*time.Millisecond)
}
