package main

import (
	"errors"
	"reflect"
	"testing"
)

const invalidStreamIDError = "-ERR Invalid stream ID specified as stream command argument\r\n"

// streamEntries returns a copy of the entries of the stream stored at key,
// read straight from the server's keyspace.
func streamEntries(t *testing.T, srv *Server, key string) []streamEntry {
	t.Helper()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	st, err := srv.store.getStream(key)
	if err != nil {
		t.Fatalf("reading stream %q: %v", key, err)
	}
	if st == nil {
		return nil
	}
	return append([]streamEntry(nil), st.entries...)
}

func TestXAddCreatesStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply is the ID of the new entry, as a bulk string.
	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddWithSeveralFields(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "XADD", "stream_key", "1526919030474-0", "temperature", "36", "humidity", "95")
	expectReply(t, conn, "$15\r\n1526919030474-0\r\n")

	want := []streamEntry{{
		id:     streamID{ms: 1526919030474, seq: 0},
		fields: []string{"temperature", "36", "humidity", "95"},
	}}
	if got := streamEntries(t, srv, "stream_key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
}

func TestXAddAppendsToExistingStream(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "XADD", "stream_key", "1-1", "a", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XADD", "stream_key", "1-2", "b", "2")
	expectReply(t, conn, "$3\r\n1-2\r\n")
	do(t, conn, "XADD", "stream_key", "2-0", "c", "3", "d", "4")
	expectReply(t, conn, "$3\r\n2-0\r\n")

	// Entries are kept in the order they were added, each with its own fields.
	want := []streamEntry{
		{id: streamID{1, 1}, fields: []string{"a", "1"}},
		{id: streamID{1, 2}, fields: []string{"b", "2"}},
		{id: streamID{2, 0}, fields: []string{"c", "3", "d", "4"}},
	}
	if got := streamEntries(t, srv, "stream_key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddStreamsAreIndependent(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "XADD", "first", "5-0", "a", "1")
	expectReply(t, conn, "$3\r\n5-0\r\n")
	// Another stream has its own entries and IDs.
	do(t, conn, "XADD", "second", "1-0", "b", "2")
	expectReply(t, conn, "$3\r\n1-0\r\n")

	if got := streamEntries(t, srv, "first"); len(got) != 1 {
		t.Fatalf("first stream holds %d entries, want 1", len(got))
	}
	if got := streamEntries(t, srv, "second"); len(got) != 1 {
		t.Fatalf("second stream holds %d entries, want 1", len(got))
	}
}

func TestXAddKeepsFieldsVerbatim(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Fields and values are binary-safe, may be empty, and a field may repeat.
	fields := []string{"", "", "with space", "a\r\nb", "\x00", "\xff", "dup", "1", "dup", "2"}
	do(t, conn, append([]string{"XADD", "stream_key", "1-1"}, fields...)...)
	expectReply(t, conn, "$3\r\n1-1\r\n")

	got := streamEntries(t, srv, "stream_key")
	if len(got) != 1 || !reflect.DeepEqual(got[0].fields, fields) {
		t.Fatalf("entries = %+v, want a single entry with fields %q", got, fields)
	}
}

func TestXAddIDFormats(t *testing.T) {
	tests := []struct {
		name, id, want string
	}{
		{"milliseconds and sequence", "1526919030474-3", "1526919030474-3"},
		{"milliseconds only means sequence 0", "1526919030474", "1526919030474-0"},
		{"zero milliseconds", "0-1", "0-1"},
		{"zero sequence", "1-0", "1-0"},
		{"leading zeros are dropped", "007-01", "7-1"},
		{"largest possible ID", "18446744073709551615-18446744073709551615", "18446744073709551615-18446744073709551615"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dial(t, startTestServer(t))

			do(t, conn, "XADD", "stream_key", tt.id, "foo", "bar")
			expectReply(t, conn, bulk(tt.want))
		})
	}
}

func TestXAddInvalidID(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, id := range []string{
		"abc", "", "-", "1-", "-1", "1-2-3", "1.5-0", "1-a", "a-1", "-1-0",
		"1-1 ", "18446744073709551616-0", "0-18446744073709551616",
	} {
		do(t, conn, "XADD", "stream_key", id, "foo", "bar")
		expectReply(t, conn, invalidStreamIDError)
	}

	// None of the rejected commands created the stream.
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")
}

func TestXAddWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const wrongArity = "-ERR wrong number of arguments for 'xadd' command\r\n"

	do(t, conn, "XADD")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XADD", "stream_key")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XADD", "stream_key", "1-1")
	expectReply(t, conn, wrongArity)
	// A field without its value.
	do(t, conn, "XADD", "stream_key", "1-1", "field")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XADD", "stream_key", "1-1", "field", "value", "orphan")
	expectReply(t, conn, wrongArity)

	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")
}

func TestXAddAgainstOtherTypesIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "XADD", "text", "1-1", "foo", "bar")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "XADD", "list_key", "1-1", "foo", "bar")
	expectReply(t, conn, wrongTypeError)

	// The existing values are untouched.
	do(t, conn, "TYPE", "text")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+list\r\n")
}

func TestOtherCommandsAgainstStreamAreWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "GET", "stream_key")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "RPUSH", "stream_key", "element")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "LRANGE", "stream_key", "0", "-1")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")

	// SET replaces the stream by a string.
	do(t, conn, "SET", "stream_key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+string\r\n")
}

func TestTypeStillReportsOtherTypes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "some_key", "foo")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	do(t, conn, "TYPE", "some_key")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "xadd", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XAdd", "stream_key", "1-2", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-2\r\n")
}

func TestParseStreamID(t *testing.T) {
	valid := []struct {
		input string
		want  streamID
	}{
		{"0-1", streamID{0, 1}},
		{"1526919030474-0", streamID{1526919030474, 0}},
		{"5", streamID{5, 0}},
		{"5-7", streamID{5, 7}},
		{"18446744073709551615-18446744073709551615", streamID{1<<64 - 1, 1<<64 - 1}},
	}
	for _, tt := range valid {
		got, err := parseStreamID(tt.input, 0)
		if err != nil || got != tt.want {
			t.Errorf("parseStreamID(%q) = (%v, %v), want (%v, nil)", tt.input, got, err, tt.want)
		}
	}

	for _, input := range []string{"", "-", "a", "1-", "-1", "1-2-3", "1-b", "1.0", "18446744073709551616"} {
		if got, err := parseStreamID(input, 0); !errors.Is(err, errInvalidStreamID) {
			t.Errorf("parseStreamID(%q) = (%v, %v), want errInvalidStreamID", input, got, err)
		}
	}
}

func TestStreamIDString(t *testing.T) {
	tests := []struct {
		id   streamID
		want string
	}{
		{streamID{0, 0}, "0-0"},
		{streamID{0, 1}, "0-1"},
		{streamID{1526919030474, 12}, "1526919030474-12"},
		{streamID{1<<64 - 1, 1<<64 - 1}, "18446744073709551615-18446744073709551615"},
	}
	for _, tt := range tests {
		if got := tt.id.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestParseStreamIDMissingSequenceNumber(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	// The default only applies when the sequence number is left out.
	if got, _ := parseStreamID("5", maxUint64); got != (streamID{5, maxUint64}) {
		t.Errorf(`parseStreamID("5", max) = %v, want 5-%d`, got, uint64(maxUint64))
	}
	if got, _ := parseStreamID("5-3", maxUint64); got != (streamID{5, 3}) {
		t.Errorf(`parseStreamID("5-3", max) = %v, want 5-3`, got)
	}
	if got, _ := parseStreamID("5-0", maxUint64); got != (streamID{5, 0}) {
		t.Errorf(`parseStreamID("5-0", max) = %v, want 5-0`, got)
	}
}
