package main

import (
	"errors"
	"testing"
)

const wrongTypeError = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"

func TestRPushCreatesList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply is the length of the list after the push, as a RESP integer.
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
}

func TestRPushCreatesIndependentLists(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Every key gets its own list: each of these pushes creates a new one.
	for i := 0; i < 5; i++ {
		do(t, conn, "RPUSH", randomWord(t), randomWord(t))
		expectReply(t, conn, ":1\r\n")
	}
}

func TestRPushCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"rpush", "RPush", "rPUSH"} {
		do(t, conn, name, "list-"+name, "element")
		expectReply(t, conn, ":1\r\n")
	}
}

func TestRPushWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH")
	expectReply(t, conn, "-ERR wrong number of arguments for 'rpush' command\r\n")
	do(t, conn, "RPUSH", "list_key")
	expectReply(t, conn, "-ERR wrong number of arguments for 'rpush' command\r\n")

	// The failed commands did not create the list.
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
}

func TestRPushAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "RPUSH", "text", "element")
	expectReply(t, conn, wrongTypeError)

	// The string is left untouched.
	do(t, conn, "GET", "text")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}

func TestGetAgainstListIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "GET", "list_key")
	expectReply(t, conn, wrongTypeError)
}

func TestSetReplacesList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "key", "element")
	expectReply(t, conn, ":1\r\n")

	// SET overwrites a key whatever it held before.
	do(t, conn, "SET", "key", "now a string")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "key")
	expectReply(t, conn, "$12\r\nnow a string\r\n")
}

func TestStoreLists(t *testing.T) {
	st := newStore()

	if l, err := st.getList("missing"); l != nil || err != nil {
		t.Fatalf(`getList("missing") = (%v, %v), want (nil, nil)`, l, err)
	}

	created, err := st.getOrCreateList("list")
	if err != nil || created == nil {
		t.Fatalf(`getOrCreateList("list") = (%v, %v), want a new list`, created, err)
	}
	created.items = append(created.items, "a")

	// The same list is returned from then on, with its content.
	again, err := st.getOrCreateList("list")
	if err != nil || again != created {
		t.Fatalf("getOrCreateList returned a different list for an existing key (err: %v)", err)
	}
	found, err := st.getList("list")
	if err != nil || found != created || len(found.items) != 1 || found.items[0] != "a" {
		t.Fatalf(`getList("list") = (%v, %v), want the list holding "a"`, found, err)
	}
}

func TestStoreReportsWrongType(t *testing.T) {
	st := newStore()
	st.setString("text", "value", noExpiry)
	if _, err := st.getOrCreateList("list"); err != nil {
		t.Fatalf("creating a list: %v", err)
	}

	if _, err := st.getList("text"); !errors.Is(err, errWrongType) {
		t.Errorf(`getList on a string: err = %v, want errWrongType`, err)
	}
	if _, err := st.getOrCreateList("text"); !errors.Is(err, errWrongType) {
		t.Errorf(`getOrCreateList on a string: err = %v, want errWrongType`, err)
	}
	if _, _, err := st.getString("list"); !errors.Is(err, errWrongType) {
		t.Errorf(`getString on a list: err = %v, want errWrongType`, err)
	}

	// A failed access must not have replaced the string by a list.
	if value, ok, err := st.getString("text"); !ok || value != "value" || err != nil {
		t.Errorf(`getString("text") = (%q, %v, %v), want ("value", true, nil)`, value, ok, err)
	}
}
