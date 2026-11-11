package main

import (
	"strconv"
	"testing"
)

func TestGetReturnsTheValueOfEachKeyOfTheRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "foo", value: "123"},
		rdbTestKey{key: "bar", value: "456"},
		rdbTestKey{key: "baz", value: "a longer value, with spaces"},
	)))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\n123\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$3\r\n456\r\n")
	do(t, conn, "GET", "baz")
	expectReply(t, conn, "$27\r\na longer value, with spaces\r\n")
	do(t, conn, "GET", "qux")
	expectReply(t, conn, "$-1\r\n")
}

func TestValuesOfTheRDBFileAreNotMixedUp(t *testing.T) {
	var keys []rdbTestKey
	for i := range 100 {
		keys = append(keys, rdbTestKey{key: "key:" + strconv.Itoa(i), value: "value:" + strconv.Itoa(i)})
	}
	conn := dial(t, startServerWithRDB(t, buildRDB(keys...)))

	for i := range 100 {
		do(t, conn, "GET", "key:"+strconv.Itoa(i))
		if got, want := readBulkString(t, conn), "value:"+strconv.Itoa(i); got != want {
			t.Errorf("key:%d = %q, want %q", i, got, want)
		}
	}
}

func TestKeysWithTheSameValueInTheRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "a", value: "same"},
		rdbTestKey{key: "b", value: "same"},
		rdbTestKey{key: "empty", value: ""},
	)))

	do(t, conn, "GET", "a")
	expectReply(t, conn, "$4\r\nsame\r\n")
	do(t, conn, "GET", "b")
	expectReply(t, conn, "$4\r\nsame\r\n")
	// An empty value isn't a missing key.
	do(t, conn, "GET", "empty")
	expectReply(t, conn, "$0\r\n\r\n")
}

func TestKeysOfTheRDBFileAreServedToEveryClient(t *testing.T) {
	addr := startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "foo", value: "1"},
		rdbTestKey{key: "bar", value: "2"},
	))

	// One connection per command, like redis-cli does.
	for key, want := range map[string]string{"foo": "$1\r\n1\r\n", "bar": "$1\r\n2\r\n"} {
		conn := dial(t, addr)
		do(t, conn, "GET", key)
		expectReply(t, conn, want)
	}
}

func TestKeysOfTheRDBFileCanBeOverwritten(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "foo", value: "1"},
		rdbTestKey{key: "bar", value: "2"},
	)))

	do(t, conn, "SET", "foo", "new")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nnew\r\n")
	do(t, conn, "GET", "bar")
	expectReply(t, conn, "$1\r\n2\r\n")
}

func TestParseRDBMultipleValues(t *testing.T) {
	keys, err := parseRDB(buildRDB(
		rdbTestKey{key: "foo", value: "123"},
		rdbTestKey{key: "bar", value: "456"},
	))
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 2 || keys[0] != (rdbKey{key: "foo", value: "123"}) || keys[1] != (rdbKey{key: "bar", value: "456"}) {
		t.Errorf("keys = %+v, want foo = 123 and bar = 456", keys)
	}
}
