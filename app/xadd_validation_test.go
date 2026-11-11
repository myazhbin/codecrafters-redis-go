package main

import (
	"errors"
	"testing"
)

const (
	streamIDTooSmallError = "-ERR The ID specified in XADD is equal or smaller than the target stream top item\r\n"
	streamIDIsZeroError   = "-ERR The ID specified in XADD must be greater than 0-0\r\n"
)

func TestXAddRejectsIDsThatAreNotGreaterThanTheLastOne(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XADD", "stream_key", "1-2", "bar", "baz")
	expectReply(t, conn, "$3\r\n1-2\r\n")

	// The exact same ID as the last entry.
	do(t, conn, "XADD", "stream_key", "1-2", "baz", "foo")
	expectReply(t, conn, streamIDTooSmallError)
	// A smaller time, even with a larger sequence number.
	do(t, conn, "XADD", "stream_key", "0-3", "baz", "foo")
	expectReply(t, conn, streamIDTooSmallError)
	// The same time with a smaller sequence number.
	do(t, conn, "XADD", "stream_key", "1-1", "baz", "foo")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", "1-0", "baz", "foo")
	expectReply(t, conn, streamIDTooSmallError)
	// Without sequence number the ID is 1-0.
	do(t, conn, "XADD", "stream_key", "1", "baz", "foo")
	expectReply(t, conn, streamIDTooSmallError)

	// 0-0 is never valid, and has its own error.
	do(t, conn, "XADD", "stream_key", "0-0", "baz", "foo")
	expectReply(t, conn, streamIDIsZeroError)

	// Rejected entries were not added.
	if got := streamEntries(t, srv, "stream_key"); len(got) != 2 {
		t.Fatalf("stream holds %d entries, want the 2 valid ones", len(got))
	}
}

func TestXAddAcceptsIncreasingIDs(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Each of these is strictly greater than the previous one.
	for _, id := range []string{
		"0-1",  // the smallest valid ID
		"0-2",  // same time, greater sequence number
		"1-0",  // greater time: the sequence number may start over
		"1-1",  //
		"1-5",  // gaps are fine
		"5-0",  //
		"5-1",  //
		"10-0", // compared as numbers, not as text ("10" < "5" alphabetically)
		"1526919030474-0",
		"1526919030474-18446744073709551615",
		"18446744073709551615-0",
		"18446744073709551615-18446744073709551615",
	} {
		do(t, conn, "XADD", "stream_key", id, "foo", "bar")
		expectReply(t, conn, bulk(id))
	}

	// Nothing can come after the largest possible ID.
	do(t, conn, "XADD", "stream_key", "18446744073709551615-18446744073709551615", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)

	if got := streamEntries(t, srv, "stream_key"); len(got) != 12 {
		t.Fatalf("stream holds %d entries, want 12", len(got))
	}
}

func TestXAddZeroIDOnNewStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-0", "foo", "bar")
	expectReply(t, conn, streamIDIsZeroError)
	// "0" is shorthand for 0-0.
	do(t, conn, "XADD", "stream_key", "0", "foo", "bar")
	expectReply(t, conn, streamIDIsZeroError)

	// The rejected commands did not create the stream...
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")

	// ...and the smallest valid ID is still accepted afterwards.
	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
}

func TestXAddValidationIsPerStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "first", "100-0", "foo", "bar")
	expectReply(t, conn, "$5\r\n100-0\r\n")

	// Another stream isn't constrained by the IDs of the first one.
	do(t, conn, "XADD", "second", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "XADD", "first", "50-0", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "second", "50-0", "foo", "bar")
	expectReply(t, conn, "$4\r\n50-0\r\n")
}

func TestXAddStreamIsUsableAfterRejectedID(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-5", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-5\r\n")
	do(t, conn, "XADD", "stream_key", "5-5", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", "0-0", "foo", "bar")
	expectReply(t, conn, streamIDIsZeroError)

	// A rejected ID doesn't move the top of the stream.
	do(t, conn, "XADD", "stream_key", "5-6", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-6\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddErrorPrecedence(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")

	// Too few arguments to even hold an ID and a field-value pair.
	do(t, conn, "XADD", "text", "not-an-id", "field")
	expectReply(t, conn, "-ERR wrong number of arguments for 'xadd' command\r\n")
	// Then a malformed ID is reported before anything else...
	do(t, conn, "XADD", "text", "not-an-id", "field", "value", "orphan")
	expectReply(t, conn, invalidStreamIDError)
	// ...then a field without value...
	do(t, conn, "XADD", "text", "0-0", "field", "value", "orphan")
	expectReply(t, conn, "-ERR wrong number of arguments for 'xadd' command\r\n")
	// ...then the 0-0 ID, which is checked before looking at the key...
	do(t, conn, "XADD", "text", "0-0", "field", "value")
	expectReply(t, conn, streamIDIsZeroError)
	// ...and finally the type of the key.
	do(t, conn, "XADD", "text", "0-1", "field", "value")
	expectReply(t, conn, wrongTypeError)
}

func TestStreamIDCompare(t *testing.T) {
	tests := []struct {
		a, b streamID
		want int
	}{
		{streamID{1, 1}, streamID{1, 1}, 0},
		{streamID{0, 0}, streamID{0, 0}, 0},
		{streamID{1, 1}, streamID{1, 2}, -1},
		{streamID{1, 2}, streamID{1, 1}, 1},
		{streamID{1, 9}, streamID{2, 0}, -1},
		{streamID{2, 0}, streamID{1, 9}, 1},
		{streamID{0, 3}, streamID{1, 2}, -1},
		{streamID{5, 0}, streamID{10, 0}, -1},
		{streamID{1<<64 - 1, 0}, streamID{1<<63 - 1, 1<<64 - 1}, 1},
		{streamID{0, 1<<64 - 1}, streamID{0, 1 << 63}, 1},
	}
	for _, tt := range tests {
		if got := tt.a.compare(tt.b); got != tt.want {
			t.Errorf("%v.compare(%v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestStreamAdd(t *testing.T) {
	st := &stream{}

	// An empty stream's last ID is 0-0, so 0-0 itself is too small.
	if err := st.add(streamID{0, 0}, nil); !errors.Is(err, errStreamIDTooSmall) {
		t.Fatalf("add(0-0) on an empty stream: err = %v, want errStreamIDTooSmall", err)
	}
	if err := st.add(streamID{1, 1}, []string{"a", "1"}); err != nil {
		t.Fatalf("add(1-1): %v", err)
	}
	if err := st.add(streamID{1, 1}, nil); !errors.Is(err, errStreamIDTooSmall) {
		t.Fatalf("add(1-1) twice: err = %v, want errStreamIDTooSmall", err)
	}
	if err := st.add(streamID{0, 9}, nil); !errors.Is(err, errStreamIDTooSmall) {
		t.Fatalf("add(0-9) after 1-1: err = %v, want errStreamIDTooSmall", err)
	}
	if err := st.add(streamID{2, 0}, []string{"b", "2"}); err != nil {
		t.Fatalf("add(2-0): %v", err)
	}

	if len(st.entries) != 2 || st.entries[0].id != (streamID{1, 1}) || st.entries[1].id != (streamID{2, 0}) {
		t.Fatalf("entries = %+v, want 1-1 then 2-0", st.entries)
	}
	if st.lastID != (streamID{2, 0}) {
		t.Fatalf("lastID = %v, want 2-0", st.lastID)
	}
}
