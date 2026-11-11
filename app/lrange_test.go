package main

import (
	"reflect"
	"strconv"
	"testing"
)

const emptyArray = "*0\r\n"

func TestLRange(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "RPUSH", "list_key", "a", "b", "c", "d", "e")
	expectReply(t, conn, ":5\r\n")

	tests := []struct {
		name        string
		start, stop string
		want        []string
	}{
		{"first two elements", "0", "1", []string{"a", "b"}},
		{"first three elements", "0", "2", []string{"a", "b", "c"}},
		{"middle to end", "2", "4", []string{"c", "d", "e"}},
		{"whole list", "0", "4", []string{"a", "b", "c", "d", "e"}},
		{"single element: start equals stop", "3", "3", []string{"d"}},
		{"first element only", "0", "0", []string{"a"}},
		{"last element only", "4", "4", []string{"e"}},
		{"stop past the end is the last element", "1", "100", []string{"b", "c", "d", "e"}},
		{"stop equal to the length", "0", "5", []string{"a", "b", "c", "d", "e"}},
		{"start equal to the length", "5", "10", nil},
		{"start past the end", "42", "100", nil},
		{"start greater than stop", "3", "1", nil},
		{"start greater than stop, both past the end", "9", "7", nil},
		{"indexes with an explicit plus sign", "+1", "+2", []string{"b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			do(t, conn, "LRANGE", "list_key", tt.start, tt.stop)
			expectReply(t, conn, bulkArray(tt.want...))
		})
	}
}

func TestLRangeMissingList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A list that doesn't exist reads as an empty one: an empty array, not null.
	do(t, conn, "LRANGE", "missing", "0", "1")
	expectReply(t, conn, emptyArray)
	do(t, conn, "LRANGE", "missing", "0", "0")
	expectReply(t, conn, emptyArray)

	// Reading a missing list doesn't create it.
	do(t, conn, "RPUSH", "missing", "first")
	expectReply(t, conn, ":1\r\n")
}

func TestLRangeDoesNotModifyTheList(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	for i := 0; i < 3; i++ {
		do(t, conn, "LRANGE", "list_key", "0", "1")
		expectReply(t, conn, bulkArray("a", "b"))
	}

	do(t, conn, "LRANGE", "list_key", "0", "2")
	expectReply(t, conn, bulkArray("a", "b", "c"))
	do(t, conn, "RPUSH", "list_key", "d")
	expectReply(t, conn, ":4\r\n")
}

func TestLRangeSeesAppendedElements(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "10")
	expectReply(t, conn, bulkArray("a"))

	do(t, conn, "RPUSH", "list_key", "b", "c")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "10")
	expectReply(t, conn, bulkArray("a", "b", "c"))
}

func TestLRangeReturnsElementsVerbatim(t *testing.T) {
	conn := dial(t, startTestServer(t))

	elements := []string{"", "with space", "a\r\nb", "\x00\xff", "dup", "dup"}
	do(t, conn, append([]string{"RPUSH", "list_key"}, elements...)...)
	expectReply(t, conn, ":6\r\n")

	do(t, conn, "LRANGE", "list_key", "0", "5")
	expectReply(t, conn, bulkArray(elements...))
}

func TestLRangeLargeList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	elements := make([]string, 300)
	for i := range elements {
		elements[i] = "element-" + strconv.Itoa(i)
	}
	do(t, conn, append([]string{"RPUSH", "big"}, elements...)...)
	expectReply(t, conn, ":300\r\n")

	do(t, conn, "LRANGE", "big", "0", "299")
	expectReply(t, conn, bulkArray(elements...))
	do(t, conn, "LRANGE", "big", "120", "129")
	expectReply(t, conn, bulkArray(elements[120:130]...))
}

func TestLRangeInvalidIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	const notAnInteger = "-ERR value is not an integer or out of range\r\n"

	do(t, conn, "LRANGE", "list_key", "zero", "1")
	expectReply(t, conn, notAnInteger)
	do(t, conn, "LRANGE", "list_key", "0", "one")
	expectReply(t, conn, notAnInteger)
	do(t, conn, "LRANGE", "list_key", "0.5", "1")
	expectReply(t, conn, notAnInteger)
	do(t, conn, "LRANGE", "list_key", "0", "")
	expectReply(t, conn, notAnInteger)
	do(t, conn, "LRANGE", "list_key", "0", "99999999999999999999999")
	expectReply(t, conn, notAnInteger)

	// Indexes are validated even when the list doesn't exist.
	do(t, conn, "LRANGE", "missing", "a", "b")
	expectReply(t, conn, notAnInteger)
}

func TestLRangeWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const wrongArity = "-ERR wrong number of arguments for 'lrange' command\r\n"

	do(t, conn, "LRANGE")
	expectReply(t, conn, wrongArity)
	do(t, conn, "LRANGE", "list_key")
	expectReply(t, conn, wrongArity)
	do(t, conn, "LRANGE", "list_key", "0")
	expectReply(t, conn, wrongArity)
	do(t, conn, "LRANGE", "list_key", "0", "1", "2")
	expectReply(t, conn, wrongArity)
}

func TestLRangeAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "LRANGE", "text", "0", "1")
	expectReply(t, conn, wrongTypeError)
}

func TestLRangeIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "rpush", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")

	for _, name := range []string{"lrange", "LRange", "lRANGE"} {
		do(t, conn, name, "list_key", "0", "1")
		expectReply(t, conn, bulkArray("a", "b"))
	}
}

func TestListSlice(t *testing.T) {
	l := &list{items: []string{"a", "b", "c", "d", "e"}}

	tests := []struct {
		start, stop int
		want        []string
	}{
		{0, 1, []string{"a", "b"}},
		{2, 4, []string{"c", "d", "e"}},
		{0, 4, []string{"a", "b", "c", "d", "e"}},
		{2, 2, []string{"c"}},
		{0, 99, []string{"a", "b", "c", "d", "e"}},
		{4, 99, []string{"e"}},
		{5, 99, nil},
		{3, 2, nil},
		{99, 100, nil},
	}

	for _, tt := range tests {
		got := l.slice(tt.start, tt.stop)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("slice(%d, %d) = %q, want %q", tt.start, tt.stop, got, tt.want)
		}
	}

	if got := (&list{}).slice(0, 10); len(got) != 0 {
		t.Errorf("slice of an empty list = %q, want nothing", got)
	}
}
