package main

import (
	"testing"
	"time"
)

func TestTypeOfString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "some_key", "foo")
	expectReply(t, conn, "+OK\r\n")

	// The type is a simple string, not a bulk string.
	do(t, conn, "TYPE", "some_key")
	expectReply(t, conn, "+string\r\n")
}

func TestTypeOfMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")

	// Asking for the type of a key doesn't create it.
	do(t, conn, "GET", "missing_key")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
}

func TestTypeOfList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+list\r\n")
}

func TestTypeOfStringsWithSpecialValues(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The type depends on how the key was created, not on what it contains.
	for key, value := range map[string]string{
		"empty":   "",
		"number":  "12345",
		"binary":  "\x00\xff",
		"keyword": "list",
	} {
		do(t, conn, "SET", key, value)
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, "TYPE", key)
		expectReply(t, conn, "+string\r\n")
	}
}

func TestTypeFollowsTheKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+none\r\n")

	do(t, conn, "RPUSH", "key", "element")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+list\r\n")

	// SET replaces the list by a string.
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+string\r\n")
}

func TestTypeOfEmptiedListIsNone(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "only")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$4\r\nonly\r\n")

	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+none\r\n")
}

func TestTypeOfExpiredKeyIsNone(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "some_key", "foo", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "TYPE", "some_key")
	expectReply(t, conn, "+string\r\n")

	clock.Advance(100 * time.Millisecond)
	do(t, conn, "TYPE", "some_key")
	expectReply(t, conn, "+none\r\n")
}

func TestTypeIsCaseInsensitiveButKeysAreNot(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "Key", "value")
	expectReply(t, conn, "+OK\r\n")

	for _, name := range []string{"type", "Type", "tYPE"} {
		do(t, conn, name, "Key")
		expectReply(t, conn, "+string\r\n")
	}
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+none\r\n")
}

func TestTypeWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "TYPE")
	expectReply(t, conn, "-ERR wrong number of arguments for 'type' command\r\n")
	do(t, conn, "TYPE", "one", "two")
	expectReply(t, conn, "-ERR wrong number of arguments for 'type' command\r\n")
}

func TestEntryTypeName(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{"text", "string"},
		{"", "string"},
		{&list{}, "list"},
		{&stream{}, "stream"},
	}
	for _, tt := range tests {
		if got := (entry{value: tt.value}).typeName(); got != tt.want {
			t.Errorf("typeName of %T = %q, want %q", tt.value, got, tt.want)
		}
	}
}
