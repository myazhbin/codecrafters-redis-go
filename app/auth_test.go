package main

import (
	"net"
	"strings"
	"testing"
	"time"
)

// wrongPassError is the reply to an AUTH that fails.
const wrongPassError = "-WRONGPASS invalid username-password pair or user is disabled.\r\n"

// auth sends AUTH with the given arguments and checks whether it succeeds.
func auth(t *testing.T, conn net.Conn, succeeds bool, arguments ...string) {
	t.Helper()

	do(t, conn, append([]string{"AUTH"}, arguments...)...)
	if succeeds {
		expectReply(t, conn, "+OK\r\n")
	} else {
		expectReply(t, conn, wrongPassError)
	}
}

func TestAuth(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ACL", "SETUSER", "default", ">mypassword")
	expectReply(t, conn, "+OK\r\n")

	// A wrong password is refused with an error that starts with WRONGPASS.
	do(t, conn, "AUTH", "default", "wrongpassword")
	expectReply(t, conn, "-WRONGPASS invalid username-password pair or user is disabled.\r\n")
	do(t, conn, "AUTH", "default", "mypassword")
	expectReply(t, conn, "+OK\r\n")
}

func TestAuthWithARandomPassword(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The password can't be hardcoded.
	password := randomWord(t)
	setUser(t, conn, "default", ">"+password)
	auth(t, conn, false, "default", randomWord(t))
	auth(t, conn, true, "default", password)
	// As many times as it takes.
	auth(t, conn, false, "default", password+"x")
	auth(t, conn, true, "default", password)
}

func TestAuthPasswordsAreExact(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">MyPassword")
	for _, password := range []string{"mypassword", "MYPASSWORD", "MyPassword ", " MyPassword", "MyPasswor", "MyPasswords", "", ">MyPassword",
		// The hash of the password isn't the password.
		sha256Hex("MyPassword")} {
		auth(t, conn, false, "default", password)
	}
	auth(t, conn, true, "default", "MyPassword")

	// Passwords can be any bytes, including none.
	for _, password := range []string{"", "two words", "a\r\nb", "a\x00b", "pässwörd ✓", strings.Repeat("long", 1000)} {
		setUser(t, conn, "default", "resetpass", ">"+password)
		auth(t, conn, true, "default", password)
		auth(t, conn, false, "default", password+"\x00")
	}
}

func TestAuthWithAnyOfThePasswordsOfTheUser(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">first", ">second", ">third")
	for _, password := range []string{"third", "first", "second"} {
		auth(t, conn, true, "default", password)
	}
	auth(t, conn, false, "default", "fourth")

	// A password that is removed doesn't work anymore; the others still do.
	setUser(t, conn, "default", "<second")
	auth(t, conn, false, "default", "second")
	auth(t, conn, true, "default", "first")
	auth(t, conn, true, "default", "third")
}

func TestAuthAsAUserWithoutPassword(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The default user has the nopass flag: any password will do.
	for _, password := range []string{"anything", "", "wrongpassword"} {
		auth(t, conn, true, "default", password)
	}

	// Without the flag and without password, none will.
	setUser(t, conn, "default", "resetpass")
	for _, password := range []string{"anything", "", "default"} {
		auth(t, conn, false, "default", password)
	}
	setUser(t, conn, "default", "nopass")
	auth(t, conn, true, "default", "anything")
}

func TestAuthAsAUserThatDoesNotExist(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">mypassword")
	// The error is the same as for a wrong password: it doesn't tell which
	// of the two is wrong.
	for _, name := range []string{"alice", "Default", "DEFAULT", "default ", "", "mypassword"} {
		auth(t, conn, false, name, "mypassword")
	}
	// Trying doesn't create the user.
	do(t, conn, "ACL", "GETUSER", "alice")
	expectReply(t, conn, "$-1\r\n")
}

func TestAuthAsAnotherUser(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "alice", ">secret")
	setUser(t, conn, "bob", ">hunter2")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))

	// The connection is the user it authenticated as.
	auth(t, conn, true, "alice", "secret")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("alice"))

	// Each user has its own passwords, and a failed attempt changes
	// nothing.
	auth(t, conn, false, "bob", "secret")
	auth(t, conn, false, "alice", "hunter2")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("alice"))

	auth(t, conn, true, "bob", "hunter2")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("bob"))
	auth(t, conn, true, "default", "whatever")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))
}

func TestAuthIsPerConnection(t *testing.T) {
	addr := startTestServer(t)
	first, second := dial(t, addr), dial(t, addr)

	setUser(t, first, "alice", ">secret")
	auth(t, first, true, "alice", "secret")

	// The other connection is who it was.
	do(t, second, "ACL", "WHOAMI")
	expectReply(t, second, bulk("default"))
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, bulk("alice"))
}

func TestAuthWithThePasswordAlone(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Without user name, the user is the default one, which must have a
	// password for that to mean something.
	do(t, conn, "AUTH", "mypassword")
	expectReply(t, conn, "-ERR AUTH <password> called without any password configured for the default user. Are you sure your configuration is correct?\r\n")

	setUser(t, conn, "default", ">mypassword")
	auth(t, conn, false, "wrongpassword")
	auth(t, conn, false, "default")
	auth(t, conn, true, "mypassword")

	// It is the default user whoever the connection was before.
	setUser(t, conn, "alice", ">secret")
	auth(t, conn, true, "alice", "secret")
	auth(t, conn, false, "secret")
	auth(t, conn, true, "mypassword")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, bulk("default"))
}

func TestAuthInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "AUTH")
	expectReply(t, conn, "-ERR wrong number of arguments for 'auth' command\r\n")
	do(t, conn, "AUTH", "default", "mypassword", "extra")
	expectReply(t, conn, "-ERR syntax error\r\n")

	// The name of the command is case-insensitive; the rest isn't.
	setUser(t, conn, "default", ">mypassword")
	for _, name := range []string{"AUTH", "auth", "Auth"} {
		do(t, conn, name, "default", "mypassword")
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, name, "default", "MYPASSWORD")
		expectReply(t, conn, wrongPassError)
	}
}

func TestAuthInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "alice", ">secret")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"AUTH", "alice", "wrong"},
		{"ACL", "WHOAMI"},
		{"AUTH", "alice", "secret"},
		{"ACL", "WHOAMI"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(wrongPassError, bulk("default"), "+OK\r\n", bulk("alice")))
}

func TestAuthInSubscribedMode(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	do(t, conn, "AUTH", "default", "anything")
	expectReply(t, conn, notAllowedInSubscribedMode("auth"))
}

func TestAuthLeavesNoTraceOfThePassword(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	setUser(t, conn, "default", ">mypassword")
	auth(t, conn, false, "default", "wrongpassword")
	auth(t, conn, true, "default", "mypassword")
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")

	// The write that follows is all there is in the log and in the
	// replication stream: authenticating isn't a write, and passwords don't
	// travel.
	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "bar"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectPropagated(t, replica, "SET", "foo", "bar")
	expectNoReply(t, replica, 50*time.Millisecond)
}
