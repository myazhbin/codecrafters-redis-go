package main

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestXAddAutoSequenceNumber(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// With a time part of 0 the sequence number starts at 1, because 0-0 is
	// not a valid ID.
	do(t, conn, "XADD", "stream_key", "0-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	// For any other time part it starts at 0...
	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-0\r\n")

	// ...and is incremented for each entry added with the same time part.
	do(t, conn, "XADD", "stream_key", "5-*", "bar", "baz")
	expectReply(t, conn, "$3\r\n5-1\r\n")
	do(t, conn, "XADD", "stream_key", "5-*", "baz", "qux")
	expectReply(t, conn, "$3\r\n5-2\r\n")
}

func TestXAddAutoSequenceNumberOnNewStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "some_key", "1-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-0\r\n")
	do(t, conn, "XADD", "some_key", "1-*", "bar", "baz")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "TYPE", "some_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddAutoSequenceNumberWithRandomTime(t *testing.T) {
	conn := dial(t, startTestServer(t))

	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("generating a random time: %v", err)
	}
	ms := strconv.FormatUint(uint64(binary.BigEndian.Uint32(raw[:]))+1, 10)

	for seq := 0; seq < 4; seq++ {
		do(t, conn, "XADD", "stream_key", ms+"-*", "foo", "bar")
		expectReply(t, conn, bulk(ms+"-"+strconv.Itoa(seq)))
	}
}

func TestXAddAutoSequenceNumberRepeatedlyAtTimeZero(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-2\r\n")
	do(t, conn, "XADD", "stream_key", "0-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-3\r\n")
}

func TestXAddAutoSequenceNumberContinuesAfterExplicitIDs(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-7", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-7\r\n")
	// Same time part as the last entry: its sequence number plus one.
	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-8\r\n")

	// Explicit IDs keep working, and are still validated.
	do(t, conn, "XADD", "stream_key", "5-20", "foo", "bar")
	expectReply(t, conn, "$4\r\n5-20\r\n")
	do(t, conn, "XADD", "stream_key", "5-20", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, "$4\r\n5-21\r\n")

	// A new time part starts over at 0.
	do(t, conn, "XADD", "stream_key", "6-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n6-0\r\n")
	do(t, conn, "XADD", "stream_key", "100-*", "foo", "bar")
	expectReply(t, conn, "$5\r\n100-0\r\n")
}

func TestXAddAutoSequenceNumberWithTimeInThePast(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-0\r\n")

	// No sequence number can make an ID with an older time part valid.
	do(t, conn, "XADD", "stream_key", "4-*", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", "0-*", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)

	if got := streamEntries(t, srv, "stream_key"); len(got) != 1 {
		t.Fatalf("stream holds %d entries, want 1", len(got))
	}
	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-1\r\n")
}

func TestXAddAutoSequenceNumberCannotOverflow(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-18446744073709551615", "foo", "bar")
	expectReply(t, conn, "$22\r\n5-18446744073709551615\r\n")

	// There is no sequence number left for this millisecond.
	do(t, conn, "XADD", "stream_key", "5-*", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", "6-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n6-0\r\n")
}

func TestXAddAutoSequenceNumberIsPerStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "first", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-0\r\n")
	do(t, conn, "XADD", "first", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-1\r\n")

	do(t, conn, "XADD", "second", "5-*", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-0\r\n")
	do(t, conn, "XADD", "second", "3-*", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
}

func TestXAddAutoSequenceNumberFailureDoesNotCreateStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-*", "orphan-field")
	expectReply(t, conn, "-ERR wrong number of arguments for 'xadd' command\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")
}

func TestXAddInvalidPartialIDs(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, id := range []string{"*-*", "*-1", "-*", "5-**", "5--*", "a-*", "5-*-", "1.5-*", "18446744073709551616-*"} {
		do(t, conn, "XADD", "stream_key", id, "foo", "bar")
		expectReply(t, conn, invalidStreamIDError)
	}
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")
}

func TestParseNewEntryID(t *testing.T) {
	valid := []struct {
		input string
		want  newEntryID
	}{
		{"5-3", newEntryID{id: streamID{5, 3}}},
		{"5", newEntryID{id: streamID{5, 0}}},
		{"0-0", newEntryID{id: streamID{0, 0}}},
		{"5-*", newEntryID{id: streamID{5, 0}, autoSeq: true}},
		{"0-*", newEntryID{id: streamID{0, 0}, autoSeq: true}},
		{"18446744073709551615-*", newEntryID{id: streamID{1<<64 - 1, 0}, autoSeq: true}},
	}
	for _, tt := range valid {
		got, err := parseNewEntryID(tt.input)
		if err != nil || got != tt.want {
			t.Errorf("parseNewEntryID(%q) = (%+v, %v), want (%+v, nil)", tt.input, got, err, tt.want)
		}
	}

	for _, input := range []string{"", "-*", "*-*", "*-5", "a-*", "5-**", "5-a"} {
		if got, err := parseNewEntryID(input); !errors.Is(err, errInvalidStreamID) {
			t.Errorf("parseNewEntryID(%q) = (%+v, %v), want errInvalidStreamID", input, got, err)
		}
	}
}

func TestStreamAddAutoSequenceNumber(t *testing.T) {
	st := &stream{}
	auto := func(ms uint64) newEntryID { return newEntryID{id: streamID{ms: ms}, autoSeq: true} }

	steps := []struct {
		spec newEntryID
		want streamID
	}{
		{auto(0), streamID{0, 1}},
		{auto(0), streamID{0, 2}},
		{auto(5), streamID{5, 0}},
		{auto(5), streamID{5, 1}},
		{newEntryID{id: streamID{5, 10}}, streamID{5, 10}},
		{auto(5), streamID{5, 11}},
		{auto(7), streamID{7, 0}},
	}
	for i, step := range steps {
		got, err := st.add(step.spec, nil, time.Time{})
		if err != nil || got != step.want {
			t.Fatalf("step %d: add(%+v) = (%v, %v), want (%v, nil)", i, step.spec, got, err, step.want)
		}
	}

	if _, err := st.add(auto(6), nil, time.Time{}); !errors.Is(err, errStreamIDTooSmall) {
		t.Fatalf("add(6-*) after 7-0: err = %v, want errStreamIDTooSmall", err)
	}
	if len(st.entries) != len(steps) {
		t.Fatalf("stream holds %d entries, want %d", len(st.entries), len(steps))
	}
}
