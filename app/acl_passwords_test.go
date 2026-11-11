package main

import (
	"reflect"
	"testing"
)

// passwordsOf returns the hashes of the passwords found among the properties
// ACL GETUSER reports for a user.
func passwordsOf(t *testing.T, properties []userProperty) []string {
	t.Helper()

	if len(properties) < 2 || properties[1].name != "passwords" {
		t.Fatalf("properties = %v, want the passwords in second position", properties)
	}
	return properties[1].values
}

func TestACLGetUserReportsThePasswords(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The default user has the nopass flag, and no password.
	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, "*4\r\n$5\r\nflags\r\n*1\r\n$6\r\nnopass\r\n$9\r\npasswords\r\n*0\r\n")
}

func TestPropertiesOfTheDefaultUser(t *testing.T) {
	conn := dial(t, startTestServer(t))

	properties := getUser(t, conn, "default")
	want := []userProperty{
		{name: "flags", values: []string{"nopass"}},
		{name: "passwords", values: []string{}},
	}
	if !reflect.DeepEqual(properties, want) {
		t.Errorf("properties of the default user = %v, want %v", properties, want)
	}
	if got := passwordsOf(t, properties); len(got) != 0 {
		t.Errorf("passwords of the default user = %q, want none", got)
	}
}

func TestPasswordsAreReportedTheSameToEveryConnection(t *testing.T) {
	addr := startTestServer(t)

	for range 3 {
		conn := dial(t, addr)
		do(t, conn, "ACL", "GETUSER", "default")
		expectReply(t, conn, array(bulk("flags"), bulkArray("nopass"), bulk("passwords"), "*0\r\n"))
	}
	// Still nothing for a user that doesn't exist.
	conn := dial(t, addr)
	do(t, conn, "ACL", "GETUSER", "alice")
	expectReply(t, conn, "$-1\r\n")
}

func TestPasswordsPropertyInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ACL", "GETUSER", "default")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "ACL", "WHOAMI")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(
		array(bulk("flags"), bulkArray("nopass"), bulk("passwords"), "*0\r\n"),
		bulk("default"),
	))
}
