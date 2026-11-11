package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// sha256Hex returns the SHA-256 hash of s in lower-case hexadecimal, computed
// independently of the server.
func sha256Hex(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

// setUser sends ACL SETUSER with the rules, which must be accepted.
func setUser(t *testing.T, conn net.Conn, name string, rules ...string) {
	t.Helper()

	do(t, conn, append([]string{"ACL", "SETUSER", name}, rules...)...)
	expectReply(t, conn, "+OK\r\n")
}

// expectUser checks the flags and the password hashes ACL GETUSER reports
// for the user.
func expectUser(t *testing.T, conn net.Conn, name string, flags, passwords []string) {
	t.Helper()

	got := getUser(t, conn, name)
	want := []userProperty{
		{name: "flags", values: append([]string{}, flags...)},
		{name: "passwords", values: append([]string{}, passwords...)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("properties of %s = %v, want %v", name, got, want)
	}
}

func TestACLSetUserSetsThePasswordOfTheDefaultUser(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, "*4\r\n$5\r\nflags\r\n*1\r\n$6\r\nnopass\r\n$9\r\npasswords\r\n*0\r\n")

	do(t, conn, "ACL", "SETUSER", "default", ">mypassword")
	expectReply(t, conn, "+OK\r\n")

	// The nopass flag is gone, and the password is there as its SHA-256
	// hash.
	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, "*4\r\n$5\r\nflags\r\n*0\r\n$9\r\npasswords\r\n*1\r\n"+
		"$64\r\n89e01536ac207279409d4de1e5253e01f4a1769e696db0d6062ca9b8f56767c8\r\n")
}

func TestPasswordIsStoredAsItsHash(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The hash can't be hardcoded: it is the one of the password.
	password := randomWord(t)
	setUser(t, conn, "default", ">"+password)
	expectUser(t, conn, "default", nil, []string{sha256Hex(password)})

	// The password itself is nowhere to be found.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	u := srv.users["default"]
	if u.nopass || len(u.passwords) != 1 || u.passwords[0] != sha256Hex(password) || strings.Contains(u.passwords[0], password) {
		t.Errorf("the default user is %+v, want it to hold the hash of its password and nothing else", *u)
	}
}

func TestPasswordsCanBeAnyBytes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// What follows the ">" is the password, whatever it is.
	passwords := []string{"", "two words", ">starts with the sign", "a\r\nb", "a\x00b", "pässwörd ✓", strings.Repeat("long", 1000), "nopass", "MyPassword", "mypassword"}
	var hashes []string
	for _, password := range passwords {
		setUser(t, conn, "default", ">"+password)
		hashes = append(hashes, sha256Hex(password))
	}
	// A user can have several passwords, which are listed in the order they
	// were added.
	expectUser(t, conn, "default", nil, hashes)
	if hashes[0] != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("hash of the empty password = %q, want the SHA-256 hash of nothing", hashes[0])
	}
}

func TestAddingAPasswordTwice(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">first")
	setUser(t, conn, "default", ">second")
	// The password is there already: nothing changes, and that's fine.
	setUser(t, conn, "default", ">first")
	setUser(t, conn, "default", ">second", ">second", ">first")
	expectUser(t, conn, "default", nil, []string{sha256Hex("first"), sha256Hex("second")})
}

func TestRemovingAPassword(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">first", ">second", ">third")
	setUser(t, conn, "default", "<second")
	expectUser(t, conn, "default", nil, []string{sha256Hex("first"), sha256Hex("third")})

	// A password the user doesn't have can't be removed.
	do(t, conn, "ACL", "SETUSER", "default", "<second")
	expectReply(t, conn, "-ERR Error in ACL SETUSER modifier '<second': The password you are trying to remove from the user does not exist\r\n")
	do(t, conn, "ACL", "SETUSER", "default", "<First")
	expectReply(t, conn, "-ERR Error in ACL SETUSER modifier '<First': The password you are trying to remove from the user does not exist\r\n")

	// Removing the last password doesn't bring the nopass flag back: the
	// user is left without any way of authenticating.
	setUser(t, conn, "default", "<first", "<third")
	expectUser(t, conn, "default", nil, nil)
}

func TestNopassAndResetpassRules(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">first", ">second")
	expectUser(t, conn, "default", nil, []string{sha256Hex("first"), sha256Hex("second")})

	// nopass removes the passwords: any password will do from now on.
	setUser(t, conn, "default", "nopass")
	expectUser(t, conn, "default", []string{"nopass"}, nil)
	setUser(t, conn, "default", "nopass")
	expectUser(t, conn, "default", []string{"nopass"}, nil)

	// resetpass removes them too, and the flag with them: no password will
	// do.
	setUser(t, conn, "default", "resetpass")
	expectUser(t, conn, "default", nil, nil)
	setUser(t, conn, "default", ">first")
	setUser(t, conn, "default", "ResetPass")
	expectUser(t, conn, "default", nil, nil)
	setUser(t, conn, "default", "NOPASS")
	expectUser(t, conn, "default", []string{"nopass"}, nil)
}

func TestRulesAreAppliedInOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">a", ">b", "<a")
	expectUser(t, conn, "default", nil, []string{sha256Hex("b")})

	setUser(t, conn, "default", ">c", "nopass")
	expectUser(t, conn, "default", []string{"nopass"}, nil)
	setUser(t, conn, "default", "nopass", ">c")
	expectUser(t, conn, "default", nil, []string{sha256Hex("c")})

	setUser(t, conn, "default", "resetpass", ">d", ">e", "<d", ">f")
	expectUser(t, conn, "default", nil, []string{sha256Hex("e"), sha256Hex("f")})

	// Without rules, nothing changes.
	setUser(t, conn, "default")
	expectUser(t, conn, "default", nil, []string{sha256Hex("e"), sha256Hex("f")})
}

func TestInvalidRuleLeavesTheUserUnchanged(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setUser(t, conn, "default", ">first")

	// One rule that is wrong, wherever it is, and none of them is applied.
	for _, tt := range []struct {
		rules    []string
		modifier string
		problem  string
	}{
		{[]string{">second", "<missing"}, "<missing", "The password you are trying to remove from the user does not exist"},
		{[]string{"<first", ">second", "<third"}, "<third", "The password you are trying to remove from the user does not exist"},
		{[]string{"nopass", "nosuchrule"}, "nosuchrule", "Syntax error"},
		{[]string{"password"}, "password", "Syntax error"},
		{[]string{">second", ""}, "", "Syntax error"},
		{[]string{"resetpass", "=equal"}, "=equal", "Syntax error"},
		{[]string{" >padded"}, " >padded", "Syntax error"},
	} {
		do(t, conn, append([]string{"ACL", "SETUSER", "default"}, tt.rules...)...)
		expectReply(t, conn, "-ERR Error in ACL SETUSER modifier '"+tt.modifier+"': "+tt.problem+"\r\n")
		expectUser(t, conn, "default", nil, []string{sha256Hex("first")})
	}
}

func TestACLSetUserCreatesUsers(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A user that doesn't exist is created, without password and without
	// the nopass flag: it takes a password to be that user.
	setUser(t, conn, "alice", ">secret")
	expectUser(t, conn, "alice", nil, []string{sha256Hex("secret")})
	setUser(t, conn, "bob")
	expectUser(t, conn, "bob", nil, nil)
	setUser(t, conn, "carol", "nopass")
	expectUser(t, conn, "carol", []string{"nopass"}, nil)

	// Each user has its own passwords.
	setUser(t, conn, "alice", ">another")
	setUser(t, conn, "bob", ">secret")
	expectUser(t, conn, "alice", nil, []string{sha256Hex("secret"), sha256Hex("another")})
	expectUser(t, conn, "bob", nil, []string{sha256Hex("secret")})
	expectUser(t, conn, "default", []string{"nopass"}, nil)

	// A user whose creation fails isn't created.
	do(t, conn, "ACL", "SETUSER", "dave", ">secret", "nosuchrule")
	expectReply(t, conn, "-ERR Error in ACL SETUSER modifier 'nosuchrule': Syntax error\r\n")
	do(t, conn, "ACL", "GETUSER", "dave")
	expectReply(t, conn, "$-1\r\n")

	// User names are exact.
	do(t, conn, "ACL", "GETUSER", "Alice")
	expectReply(t, conn, "$-1\r\n")
}

func TestACLSetUserInvalidUserNames(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"two words", " alice", "alice ", "tab\tbed", "new\nline", "nul\x00byte", " "} {
		do(t, conn, "ACL", "SETUSER", name, ">secret")
		expectReply(t, conn, "-ERR Usernames can't contain spaces or null characters\r\n")
		do(t, conn, "ACL", "GETUSER", name)
		expectReply(t, conn, "$-1\r\n")
	}
}

func TestACLSetUserInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ACL", "SETUSER")
	expectReply(t, conn, "-ERR wrong number of arguments for 'acl|setuser' command\r\n")
	// The names of the command and of the subcommand are case-insensitive.
	do(t, conn, "acl", "setuser", "default", ">mypassword")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "Acl", "SetUser", "default", ">other")
	expectReply(t, conn, "+OK\r\n")
	expectUser(t, conn, "default", nil, []string{sha256Hex("mypassword"), sha256Hex("other")})
}

func TestTheUserIsTheSameForEveryConnection(t *testing.T) {
	addr := startTestServer(t)
	first, second := dial(t, addr), dial(t, addr)

	// What one connection sets, the other one sees.
	setUser(t, first, "default", ">mypassword")
	expectUser(t, second, "default", nil, []string{sha256Hex("mypassword")})
	setUser(t, second, "default", ">other")
	expectUser(t, first, "default", nil, []string{sha256Hex("mypassword"), sha256Hex("other")})

	// The connection that set the password is still the default user.
	do(t, first, "ACL", "WHOAMI")
	expectReply(t, first, bulk("default"))
	do(t, first, "SET", "foo", "bar")
	expectReply(t, first, "+OK\r\n")
}

func TestACLSetUserInATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ACL", "SETUSER", "default", ">mypassword"},
		{"ACL", "SETUSER", "default", "nosuchrule"},
		{"ACL", "GETUSER", "default"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Nothing changes before the transaction is executed.
	expectUser(t, other, "default", []string{"nopass"}, nil)

	do(t, conn, "EXEC")
	expectReply(t, conn, array(
		"+OK\r\n",
		"-ERR Error in ACL SETUSER modifier 'nosuchrule': Syntax error\r\n",
		array(bulk("flags"), "*0\r\n", bulk("passwords"), bulkArray(sha256Hex("mypassword"))),
	))
}

func TestACLSetUserIsNotAWriteToTheDataset(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	setUser(t, conn, "default", ">mypassword")
	setUser(t, conn, "alice", ">secret")

	// Users aren't part of the dataset: they are neither logged nor
	// propagated, and they aren't keys.
	if got := readFile(t, incrPath); got != "" {
		t.Errorf("append-only file holds %q, want nothing", got)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	// A server that restarts has its default user as it is at the start.
	restarted := restartWithAOF(t, srv, args...)
	restarted.mu.Lock()
	defer restarted.mu.Unlock()
	if u := restarted.users["default"]; !u.nopass || len(u.passwords) != 0 || len(restarted.users) != 1 {
		t.Errorf("after a restart the users are %v, want only the default user, without password", restarted.users)
	}
}
