package main

import (
	"reflect"
	"strconv"
	"testing"
)

func TestLPushCreatesList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("element"))
}

func TestLPushMultipleElementsAreInsertedInReverse(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Elements are inserted at the head one by one, so the last one given
	// ends up first.
	do(t, conn, "LPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("c", "b", "a"))
}

func TestLPushPrependsToExistingList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPUSH", "list_key", "c")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LPUSH", "list_key", "b", "a")
	expectReply(t, conn, ":3\r\n")

	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("a", "b", "c"))
}

func TestLPushOneElementAtATime(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for i := 1; i <= 5; i++ {
		do(t, conn, "LPUSH", "list_key", "element-"+strconv.Itoa(i))
		expectReply(t, conn, ":"+strconv.Itoa(i)+"\r\n")
	}

	// The most recently pushed element is always at index 0.
	do(t, conn, "LRANGE", "list_key", "0", "0")
	expectReply(t, conn, bulkArray("element-5"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("element-5", "element-4", "element-3", "element-2", "element-1"))
}

func TestLPushAndRPushOnTheSameList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "c", "d")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "LPUSH", "list_key", "b")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "RPUSH", "list_key", "e")
	expectReply(t, conn, ":4\r\n")
	do(t, conn, "LPUSH", "list_key", "a", "z")
	expectReply(t, conn, ":6\r\n")

	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("z", "a", "b", "c", "d", "e"))
}

func TestLPushOnOneListLeavesOthersAlone(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPUSH", "first", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "LPUSH", "second", "x")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "LRANGE", "first", "0", "-1")
	expectReply(t, conn, bulkArray("b", "a"))
	do(t, conn, "LRANGE", "second", "0", "-1")
	expectReply(t, conn, bulkArray("x"))
}

func TestLPushKeepsElementsVerbatim(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPUSH", "list_key", "", "with space", "a\r\nb", "\x00\xff", "dup", "dup")
	expectReply(t, conn, ":6\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("dup", "dup", "\x00\xff", "a\r\nb", "with space", ""))
}

func TestLPushManyElementsAtOnce(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const count = 300
	elements := make([]string, count)
	reversed := make([]string, count)
	for i := range elements {
		elements[i] = "element-" + strconv.Itoa(i)
		reversed[count-1-i] = elements[i]
	}

	do(t, conn, append([]string{"LPUSH", "big"}, elements...)...)
	expectReply(t, conn, ":300\r\n")
	do(t, conn, "LRANGE", "big", "0", "-1")
	expectReply(t, conn, bulkArray(reversed...))
}

func TestLPushWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPUSH")
	expectReply(t, conn, "-ERR wrong number of arguments for 'lpush' command\r\n")
	do(t, conn, "LPUSH", "list_key")
	expectReply(t, conn, "-ERR wrong number of arguments for 'lpush' command\r\n")

	// The failed commands did not create the list.
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, emptyArray)
}

func TestLPushAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "LPUSH", "text", "a", "b")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "GET", "text")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}

func TestLPushIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for i, name := range []string{"lpush", "LPush", "lPUSH"} {
		do(t, conn, name, "list_key", "element")
		expectReply(t, conn, ":"+strconv.Itoa(i+1)+"\r\n")
	}
}

func TestListPush(t *testing.T) {
	l := &list{}

	l.pushBack("c")
	l.pushBack("d", "e")
	l.pushFront("b")
	l.pushFront("a", "z", "y")
	l.pushBack()
	l.pushFront()

	want := []string{"y", "z", "a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(l.items, want) {
		t.Fatalf("items = %q, want %q", l.items, want)
	}
}

func TestListPushFrontDoesNotCorruptEarlierSlices(t *testing.T) {
	l := &list{}
	l.pushBack("b", "c")

	// A range handed out before a push keeps showing the old content.
	before := l.slice(0, -1)
	l.pushFront("a")

	if want := []string{"b", "c"}; !reflect.DeepEqual(before, want) {
		t.Fatalf("slice taken before the push = %q, want %q", before, want)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(l.items, want) {
		t.Fatalf("items = %q, want %q", l.items, want)
	}
}
