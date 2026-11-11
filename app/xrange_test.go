package main

import (
	"reflect"
	"testing"
)

func TestXRange(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-2", "bar", "baz")
	expectReply(t, conn, "$3\r\n0-2\r\n")
	do(t, conn, "XADD", "stream_key", "0-3", "baz", "foo")
	expectReply(t, conn, "$3\r\n0-3\r\n")

	// Both ends of the range are included.
	do(t, conn, "XRANGE", "stream_key", "0-2", "0-3")
	expectReply(t, conn, array(
		entryReply("0-2", "bar", "baz"),
		entryReply("0-3", "baz", "foo"),
	))
}

func TestXRangeExactEncoding(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "some_key", "1526985054069-0", "temperature", "36", "humidity", "95")
	expectReply(t, conn, "$15\r\n1526985054069-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054079-0", "temperature", "37", "humidity", "94")
	expectReply(t, conn, "$15\r\n1526985054079-0\r\n")

	// An array of entries; each entry is [ID, [field, value, ...]].
	do(t, conn, "XRANGE", "some_key", "1526985054069", "1526985054079")
	expectReply(t, conn, ""+
		"*2\r\n"+
		"*2\r\n"+
		"$15\r\n1526985054069-0\r\n"+
		"*4\r\n"+
		"$11\r\ntemperature\r\n"+
		"$2\r\n36\r\n"+
		"$8\r\nhumidity\r\n"+
		"$2\r\n95\r\n"+
		"*2\r\n"+
		"$15\r\n1526985054079-0\r\n"+
		"*4\r\n"+
		"$11\r\ntemperature\r\n"+
		"$2\r\n37\r\n"+
		"$8\r\nhumidity\r\n"+
		"$2\r\n94\r\n")
}

func TestXRangeBounds(t *testing.T) {
	conn := dial(t, startTestServer(t))

	ids := []string{"1-0", "1-1", "2-0", "2-5", "3-0", "10-0"}
	for _, id := range ids {
		do(t, conn, "XADD", "stream_key", id, "id", id)
		expectReply(t, conn, bulk(id))
	}

	tests := []struct {
		name       string
		start, end string
		want       []string
	}{
		{"whole stream", "1-0", "10-0", ids},
		{"range larger than the stream", "0-1", "999-0", ids},
		{"single entry: start equals end", "2-0", "2-0", []string{"2-0"}},
		{"first entry only", "1-0", "1-0", []string{"1-0"}},
		{"last entry only", "10-0", "10-0", []string{"10-0"}},
		{"bounds that are not entries themselves", "1-5", "2-9", []string{"2-0", "2-5"}},
		{"start before the first entry", "0-0", "1-1", []string{"1-0", "1-1"}},
		{"end after the last entry", "3-0", "50-0", []string{"3-0", "10-0"}},
		{"between two entries", "3-1", "9-0", nil},
		{"before the first entry", "0-0", "0-5", nil},
		{"after the last entry", "10-1", "20-0", nil},
		{"start after end", "3-0", "2-0", nil},
		{"IDs are compared as numbers", "3-0", "10-0", []string{"3-0", "10-0"}},
		{"start without sequence number begins at sequence 0", "2", "2-5", []string{"2-0", "2-5"}},
		{"end without sequence number covers the whole millisecond", "2-0", "2", []string{"2-0", "2-5"}},
		{"both without sequence number", "1", "2", []string{"1-0", "1-1", "2-0", "2-5"}},
		{"one millisecond", "1", "1", []string{"1-0", "1-1"}},
		{"largest possible end", "2-5", "18446744073709551615-18446744073709551615", []string{"2-5", "3-0", "10-0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]string, len(tt.want))
			for i, id := range tt.want {
				entries[i] = entryReply(id, "id", id)
			}

			do(t, conn, "XRANGE", "stream_key", tt.start, tt.end)
			expectReply(t, conn, array(entries...))
		})
	}
}

func TestXRangeFromTheBeginning(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-2", "bar", "baz")
	expectReply(t, conn, "$3\r\n0-2\r\n")
	do(t, conn, "XADD", "stream_key", "0-3", "baz", "foo")
	expectReply(t, conn, "$3\r\n0-3\r\n")

	// "-" as the start: from the first entry, whatever its ID is.
	do(t, conn, "XRANGE", "stream_key", "-", "0-2")
	expectReply(t, conn, array(
		entryReply("0-1", "foo", "bar"),
		entryReply("0-2", "bar", "baz"),
	))
}

func TestXRangeFromTheBeginningWithMillisecondEnd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "some_key", "1526985054069-0", "temperature", "36", "humidity", "95")
	expectReply(t, conn, "$15\r\n1526985054069-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054079-0", "temperature", "37", "humidity", "94")
	expectReply(t, conn, "$15\r\n1526985054079-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054089-0", "temperature", "38", "humidity", "93")
	expectReply(t, conn, "$15\r\n1526985054089-0\r\n")

	do(t, conn, "XRANGE", "some_key", "-", "1526985054079")
	expectReply(t, conn, array(
		entryReply("1526985054069-0", "temperature", "36", "humidity", "95"),
		entryReply("1526985054079-0", "temperature", "37", "humidity", "94"),
	))
}

func TestXRangeFromTheBeginningBounds(t *testing.T) {
	conn := dial(t, startTestServer(t))

	ids := []string{"5-0", "5-1", "7-0", "9-3"}
	for _, id := range ids {
		do(t, conn, "XADD", "stream_key", id, "id", id)
		expectReply(t, conn, bulk(id))
	}

	tests := []struct {
		name string
		end  string
		want []string
	}{
		{"up to the first entry", "5-0", []string{"5-0"}},
		{"up to an entry in the middle", "7-0", []string{"5-0", "5-1", "7-0"}},
		{"up to the last entry", "9-3", ids},
		{"up to an ID past the last entry", "100-0", ids},
		{"up to an ID that is not an entry", "8-0", []string{"5-0", "5-1", "7-0"}},
		{"up to a whole millisecond", "5", []string{"5-0", "5-1"}},
		{"up to an ID before the first entry", "4-9", nil},
		{"up to the smallest ID", "0-0", nil},
		{"up to the beginning itself", "-", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]string, len(tt.want))
			for i, id := range tt.want {
				entries[i] = entryReply(id, "id", id)
			}

			do(t, conn, "XRANGE", "stream_key", "-", tt.end)
			expectReply(t, conn, array(entries...))
		})
	}
}

func TestXRangeFromTheBeginningIncludesTheSmallestID(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// 0-1 is the smallest ID an entry can have.
	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XRANGE", "stream_key", "-", "0-1")
	expectReply(t, conn, array(entryReply("0-1", "foo", "bar")))
}

func TestXRangeFromTheBeginningOfMissingStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XRANGE", "missing", "-", "5-0")
	expectReply(t, conn, "*0\r\n")
}

func TestXRangeFromTheBeginningAgainstOtherTypesIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "XRANGE", "text", "-", "5-0")
	expectReply(t, conn, wrongTypeError)
}

func TestXRangeToTheEnd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")
	do(t, conn, "XADD", "stream_key", "0-2", "bar", "baz")
	expectReply(t, conn, "$3\r\n0-2\r\n")
	do(t, conn, "XADD", "stream_key", "0-3", "baz", "foo")
	expectReply(t, conn, "$3\r\n0-3\r\n")

	// "+" as the end: up to the last entry, whatever its ID is.
	do(t, conn, "XRANGE", "stream_key", "0-2", "+")
	expectReply(t, conn, ""+
		"*2\r\n"+
		"*2\r\n"+
		"$3\r\n0-2\r\n"+
		"*2\r\n"+
		"$3\r\nbar\r\n"+
		"$3\r\nbaz\r\n"+
		"*2\r\n"+
		"$3\r\n0-3\r\n"+
		"*2\r\n"+
		"$3\r\nbaz\r\n"+
		"$3\r\nfoo\r\n")
}

func TestXRangeToTheEndWithMillisecondStart(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "some_key", "1526985054059-0", "temperature", "35", "humidity", "96")
	expectReply(t, conn, "$15\r\n1526985054059-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054069-0", "temperature", "36", "humidity", "95")
	expectReply(t, conn, "$15\r\n1526985054069-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054079-0", "temperature", "37", "humidity", "94")
	expectReply(t, conn, "$15\r\n1526985054079-0\r\n")

	do(t, conn, "XRANGE", "some_key", "1526985054069", "+")
	expectReply(t, conn, array(
		entryReply("1526985054069-0", "temperature", "36", "humidity", "95"),
		entryReply("1526985054079-0", "temperature", "37", "humidity", "94"),
	))
}

func TestXRangeToTheEndBounds(t *testing.T) {
	conn := dial(t, startTestServer(t))

	ids := []string{"5-0", "5-1", "7-0", "9-3"}
	for _, id := range ids {
		do(t, conn, "XADD", "stream_key", id, "id", id)
		expectReply(t, conn, bulk(id))
	}

	tests := []struct {
		name  string
		start string
		want  []string
	}{
		{"from the first entry", "5-0", ids},
		{"from an entry in the middle", "5-1", []string{"5-1", "7-0", "9-3"}},
		{"from the last entry", "9-3", []string{"9-3"}},
		{"from an ID before the first entry", "0-1", ids},
		{"from an ID that is not an entry", "6-0", []string{"7-0", "9-3"}},
		{"from a whole millisecond", "7", []string{"7-0", "9-3"}},
		{"from an ID past the last entry", "9-4", nil},
		{"from the end itself", "+", nil},
		{"from the beginning: the whole stream", "-", ids},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]string, len(tt.want))
			for i, id := range tt.want {
				entries[i] = entryReply(id, "id", id)
			}

			do(t, conn, "XRANGE", "stream_key", tt.start, "+")
			expectReply(t, conn, array(entries...))
		})
	}
}

func TestXRangeToTheEndIncludesTheLargestID(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const largest = "18446744073709551615-18446744073709551615"
	do(t, conn, "XADD", "stream_key", largest, "foo", "bar")
	expectReply(t, conn, bulk(largest))

	do(t, conn, "XRANGE", "stream_key", "0-1", "+")
	expectReply(t, conn, array(entryReply(largest, "foo", "bar")))
	do(t, conn, "XRANGE", "stream_key", "+", "+")
	expectReply(t, conn, array(entryReply(largest, "foo", "bar")))
}

func TestXRangeToTheEndSeesNewEntries(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "a", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XRANGE", "stream_key", "1-1", "+")
	expectReply(t, conn, array(entryReply("1-1", "a", "1")))

	// The end of the stream moves as entries are added.
	do(t, conn, "XADD", "stream_key", "2-0", "b", "2")
	expectReply(t, conn, "$3\r\n2-0\r\n")
	do(t, conn, "XRANGE", "stream_key", "1-1", "+")
	expectReply(t, conn, array(entryReply("1-1", "a", "1"), entryReply("2-0", "b", "2")))
}

func TestXRangeWithBothSpecialIDsInReverse(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "a", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	// From the end to the beginning: nothing is in that range.
	do(t, conn, "XRANGE", "stream_key", "+", "-")
	expectReply(t, conn, "*0\r\n")
}

func TestXRangeToTheEndOfMissingStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XRANGE", "missing", "0-1", "+")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "XRANGE", "missing", "-", "+")
	expectReply(t, conn, "*0\r\n")
}

func TestXRangeMissingStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XRANGE", "missing", "0-1", "5-0")
	expectReply(t, conn, "*0\r\n")

	// Querying doesn't create the stream.
	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestXRangeKeepsFieldsInOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	fields := []string{"z", "1", "a", "2", "m", "3", "a", "4", "", "", "with space", "a\r\nb"}
	do(t, conn, append([]string{"XADD", "stream_key", "1-1"}, fields...)...)
	expectReply(t, conn, "$3\r\n1-1\r\n")

	// Fields come back as they were given: not sorted, not deduplicated.
	do(t, conn, "XRANGE", "stream_key", "1-1", "1-1")
	expectReply(t, conn, array(entryReply("1-1", fields...)))
}

func TestXRangeDoesNotModifyTheStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "a", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XADD", "stream_key", "1-2", "b", "2")
	expectReply(t, conn, "$3\r\n1-2\r\n")

	want := array(entryReply("1-1", "a", "1"), entryReply("1-2", "b", "2"))
	for i := 0; i < 3; i++ {
		do(t, conn, "XRANGE", "stream_key", "1-1", "1-2")
		expectReply(t, conn, want)
	}

	// New entries show up in later queries.
	do(t, conn, "XADD", "stream_key", "1-3", "c", "3")
	expectReply(t, conn, "$3\r\n1-3\r\n")
	do(t, conn, "XRANGE", "stream_key", "1-2", "1-3")
	expectReply(t, conn, array(entryReply("1-2", "b", "2"), entryReply("1-3", "c", "3")))
}

func TestXRangeWithGeneratedIDs(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-*", "a", "1")
	expectReply(t, conn, "$3\r\n5-0\r\n")
	do(t, conn, "XADD", "stream_key", "5-*", "b", "2")
	expectReply(t, conn, "$3\r\n5-1\r\n")
	do(t, conn, "XADD", "stream_key", "*", "c", "3")
	generated := readBulkString(t, conn)

	do(t, conn, "XRANGE", "stream_key", "5", generated)
	expectReply(t, conn, array(
		entryReply("5-0", "a", "1"),
		entryReply("5-1", "b", "2"),
		entryReply(generated, "c", "3"),
	))
}

func TestXRangeInvalidIDs(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	for _, id := range []string{"abc", "", "1-", "1-2-3", "1.5", "*", "1-*", "18446744073709551616"} {
		do(t, conn, "XRANGE", "stream_key", id, "5-0")
		expectReply(t, conn, invalidStreamIDError)
		do(t, conn, "XRANGE", "stream_key", "0-1", id)
		expectReply(t, conn, invalidStreamIDError)
	}

	// IDs are validated even when the stream doesn't exist.
	do(t, conn, "XRANGE", "missing", "abc", "5-0")
	expectReply(t, conn, invalidStreamIDError)
}

func TestXRangeWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const wrongArity = "-ERR wrong number of arguments for 'xrange' command\r\n"

	do(t, conn, "XRANGE")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XRANGE", "stream_key")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XRANGE", "stream_key", "0-1")
	expectReply(t, conn, wrongArity)
}

func TestXRangeAgainstOtherTypesIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "XRANGE", "text", "0-1", "5-0")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "XRANGE", "list_key", "0-1", "5-0")
	expectReply(t, conn, wrongTypeError)
}

func TestXRangeIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	for _, name := range []string{"xrange", "XRange", "xRANGE"} {
		do(t, conn, name, "stream_key", "1-1", "1-1")
		expectReply(t, conn, array(entryReply("1-1", "foo", "bar")))
	}
}

func TestStreamBetween(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	st := &stream{}
	ids := []streamID{{1, 0}, {1, 1}, {2, 0}, {2, 5}, {3, 0}}
	for _, id := range ids {
		st.entries = append(st.entries, streamEntry{id: id})
	}

	idsOf := func(entries []streamEntry) []streamID {
		var out []streamID
		for _, e := range entries {
			out = append(out, e.id)
		}
		return out
	}

	tests := []struct {
		start, end streamID
		want       []streamID
	}{
		{streamID{0, 0}, streamID{maxUint64, maxUint64}, ids},
		{streamID{1, 0}, streamID{3, 0}, ids},
		{streamID{1, 1}, streamID{2, 0}, []streamID{{1, 1}, {2, 0}}},
		{streamID{2, 0}, streamID{2, 0}, []streamID{{2, 0}}},
		{streamID{1, 2}, streamID{2, 9}, []streamID{{2, 0}, {2, 5}}},
		{streamID{2, 1}, streamID{2, 4}, nil},
		{streamID{0, 0}, streamID{0, maxUint64}, nil},
		{streamID{3, 1}, streamID{9, 0}, nil},
		{streamID{3, 0}, streamID{1, 0}, nil},
	}
	for _, tt := range tests {
		if got := idsOf(st.between(tt.start, tt.end)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("between(%v, %v) = %v, want %v", tt.start, tt.end, got, tt.want)
		}
	}

	if got := (&stream{}).between(streamID{0, 0}, streamID{maxUint64, maxUint64}); len(got) != 0 {
		t.Errorf("between on an empty stream = %v, want nothing", got)
	}
}

func TestParseRangeIDMinimum(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	// "-" is the smallest ID, whatever the default sequence number is.
	for _, missingSeq := range []uint64{0, maxUint64} {
		if got, err := parseRangeID("-", missingSeq); err != nil || got != (streamID{0, 0}) {
			t.Errorf(`parseRangeID("-", %d) = (%v, %v), want (0-0, nil)`, missingSeq, got, err)
		}
	}
	// Everything else is parsed like a regular ID.
	if got, err := parseRangeID("5", maxUint64); err != nil || got != (streamID{5, maxUint64}) {
		t.Errorf(`parseRangeID("5", max) = (%v, %v), want 5-max`, got, err)
	}
	if got, err := parseRangeID("5-2", 0); err != nil || got != (streamID{5, 2}) {
		t.Errorf(`parseRangeID("5-2", 0) = (%v, %v), want 5-2`, got, err)
	}
	for _, input := range []string{"--", "-1", "- ", ""} {
		if got, err := parseRangeID(input, 0); err == nil {
			t.Errorf("parseRangeID(%q) = %v, want an error", input, got)
		}
	}
}

func TestParseRangeIDMaximum(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	// "+" is the largest ID, whatever the default sequence number is.
	for _, missingSeq := range []uint64{0, maxUint64} {
		if got, err := parseRangeID("+", missingSeq); err != nil || got != (streamID{maxUint64, maxUint64}) {
			t.Errorf(`parseRangeID("+", %d) = (%v, %v), want (max-max, nil)`, missingSeq, got, err)
		}
	}
	for _, input := range []string{"++", "+1", "+ ", "+-"} {
		if got, err := parseRangeID(input, 0); err == nil {
			t.Errorf("parseRangeID(%q) = %v, want an error", input, got)
		}
	}
}

func TestXAddRejectsRangeOnlyIDs(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "-" and "+" designate the ends of a stream in queries; they aren't IDs
	// an entry can be given.
	for _, id := range []string{"-", "+"} {
		do(t, conn, "XADD", "stream_key", id, "foo", "bar")
		expectReply(t, conn, invalidStreamIDError)
	}
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")
}
