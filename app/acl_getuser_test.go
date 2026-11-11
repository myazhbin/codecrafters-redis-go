package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// userProperty is a property of a user as ACL GETUSER reports it: its name
// and its value, which is an array of strings.
type userProperty struct {
	name   string
	values []string
}

// getUser sends ACL GETUSER for the user and returns its reply: the
// properties of the user, in order. The user has to exist.
func getUser(t *testing.T, conn net.Conn, name string) []userProperty {
	t.Helper()

	do(t, conn, "ACL", "GETUSER", name)
	header := readReplyLine(t, conn)
	if !strings.HasPrefix(header, "*") {
		t.Fatalf("reply to ACL GETUSER %s starts with %q, want an array", name, header)
	}
	elements, err := strconv.Atoi(header[1:])
	if err != nil || elements < 0 || elements%2 != 0 {
		t.Fatalf("reply to ACL GETUSER %s starts with %q, want an array of names and values", name, header)
	}

	properties := make([]userProperty, elements/2)
	for i := range properties {
		properties[i].name = readBulkString(t, conn)
		valueHeader := readReplyLine(t, conn)
		count, err := strconv.Atoi(strings.TrimPrefix(valueHeader, "*"))
		if !strings.HasPrefix(valueHeader, "*") || err != nil || count < 0 {
			t.Fatalf("value of the property %q starts with %q, want an array", properties[i].name, valueHeader)
		}
		properties[i].values = make([]string, count)
		for j := range properties[i].values {
			properties[i].values[j] = readBulkString(t, conn)
		}
	}
	return properties
}

// userFlags returns the flags ACL GETUSER reports for the user.
func userFlags(t *testing.T, conn net.Conn, name string) []string {
	t.Helper()

	properties := getUser(t, conn, name)
	if len(properties) == 0 || properties[0].name != "flags" {
		t.Fatalf("properties of %s = %v, want the flags first", name, properties)
	}
	return properties[0].values
}

func TestACLGetUser(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply alternates the names of the properties of the user and
	// their values. The flags come first, as an array.
	properties := getUser(t, conn, "default")
	if len(properties) == 0 {
		t.Fatalf("ACL GETUSER default replied with an empty array, want the properties of the user")
	}
	if properties[0].name != "flags" {
		t.Errorf("the first property is %q, want flags", properties[0].name)
	}

	// The reply starts the same way every time.
	do(t, conn, "ACL", "GETUSER", "default")
	header := readReplyLine(t, conn)
	if header != "*2" && header != "*4" {
		t.Errorf("reply starts with %q, want an array of one or two properties and their values", header)
	}
	expectReply(t, conn, "$5\r\nflags\r\n")
	drainPendingReply(t, conn)
}

func TestACLGetUserOfTheDefaultUserFromAnyConnection(t *testing.T) {
	addr := startTestServer(t)

	// The default user is there from the start, for everyone.
	var first []userProperty
	for i := range 3 {
		conn := dial(t, addr)
		properties := getUser(t, conn, "default")
		if i == 0 {
			first = properties
			continue
		}
		if len(properties) != len(first) || properties[0].name != first[0].name {
			t.Errorf("connection %d sees the properties %v, the first one saw %v", i, properties, first)
		}
	}
}

func TestACLGetUserOfAUserThatDoesNotExist(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// User names are exact: nothing is trimmed, and the case matters.
	for _, name := range []string{"alice", "Default", "DEFAULT", "default ", " default", "", "defaul", "*"} {
		do(t, conn, "ACL", "GETUSER", name)
		expectReply(t, conn, "$-1\r\n")
	}
	// Asking about a user doesn't create it.
	do(t, conn, "ACL", "GETUSER", "alice")
	expectReply(t, conn, "$-1\r\n")
}

func TestACLGetUserInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ACL", "GETUSER"}, {"ACL", "GETUSER", "default", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'acl|getuser' command\r\n")
	}
}

func TestACLGetUserNamesAreCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, names := range [][2]string{{"ACL", "GETUSER"}, {"acl", "getuser"}, {"Acl", "GetUser"}} {
		do(t, conn, names[0], names[1], "default")
		if got := readReplyLine(t, conn); !strings.HasPrefix(got, "*") || got == "*-1" || got == "*0" {
			t.Errorf("reply to %s %s default starts with %q, want the array of the properties of the user", names[0], names[1], got)
		}
		drainPendingReply(t, conn)
	}
}

func TestACLGetUserInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ACL", "GETUSER", "alice")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "ACL", "GETUSER")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array("$-1\r\n", "-ERR wrong number of arguments for 'acl|getuser' command\r\n", bulk("default")))
}

func TestACLGetUserInSubscribedMode(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, notAllowedInSubscribedMode("acl|getuser"))
}

func TestACLGetUserIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	getUser(t, conn, "default")
	do(t, conn, "ACL", "GETUSER", "alice")
	expectReply(t, conn, "$-1\r\n")

	// Nothing is logged or propagated, and users aren't keys.
	if got := readFile(t, incrPath); got != "" {
		t.Errorf("append-only file holds %q, want nothing", got)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "TYPE", "default")
	expectReply(t, conn, "+none\r\n")
}
