package main

import (
	"reflect"
	"testing"
)

const (
	nullArrayReply     = "*-1\r\n"
	unbalancedXReadErr = "-ERR Unbalanced 'xread' list of streams: for each stream key an ID, '+', or '$' must be specified.\r\n"
)

// streamReply returns the RESP encoding of what XREAD returns for one stream:
// an array holding the key and the array of its entries.
func streamReply(key string, entries ...string) string {
	return array(bulk(key), array(entries...))
}

func TestXRead(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "temperature", "96")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	do(t, conn, "XREAD", "STREAMS", "stream_key", "0-0")
	expectReply(t, conn, array(
		streamReply("stream_key", entryReply("0-1", "temperature", "96")),
	))
}

func TestXReadExactEncoding(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "some_key", "1526985054069-0", "temperature", "36", "humidity", "95")
	expectReply(t, conn, "$15\r\n1526985054069-0\r\n")
	do(t, conn, "XADD", "some_key", "1526985054079-0", "temperature", "37", "humidity", "94")
	expectReply(t, conn, "$15\r\n1526985054079-0\r\n")

	// An array of streams; each stream is [key, [entry, ...]] and each entry
	// is [ID, [field, value, ...]].
	do(t, conn, "XREAD", "STREAMS", "some_key", "1526985054069-0")
	expectReply(t, conn, ""+
		"*1\r\n"+
		"*2\r\n"+
		"$8\r\nsome_key\r\n"+
		"*1\r\n"+
		"*2\r\n"+
		"$15\r\n1526985054079-0\r\n"+
		"*4\r\n"+
		"$11\r\ntemperature\r\n"+
		"$2\r\n37\r\n"+
		"$8\r\nhumidity\r\n"+
		"$2\r\n94\r\n")
}

func TestXReadIsExclusive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	ids := []string{"1-0", "1-1", "2-0", "2-5", "10-0"}
	for _, id := range ids {
		do(t, conn, "XADD", "stream_key", id, "id", id)
		expectReply(t, conn, bulk(id))
	}

	tests := []struct {
		name string
		id   string
		want []string
	}{
		{"from the very beginning", "0-0", ids},
		{"zero without sequence number", "0", ids},
		{"the entry with the given ID is not included", "1-0", []string{"1-1", "2-0", "2-5", "10-0"}},
		{"after an entry in the middle", "2-0", []string{"2-5", "10-0"}},
		{"an ID that is not an entry", "2-3", []string{"2-5", "10-0"}},
		{"an ID before the first entry", "0-5", ids},
		{"without sequence number the ID is <ms>-0", "2", []string{"2-5", "10-0"}},
		{"just before the last entry", "9-9", []string{"10-0"}},
		{"IDs are compared as numbers", "3-0", []string{"10-0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]string, len(tt.want))
			for i, id := range tt.want {
				entries[i] = entryReply(id, "id", id)
			}

			do(t, conn, "XREAD", "STREAMS", "stream_key", tt.id)
			expectReply(t, conn, array(streamReply("stream_key", entries...)))
		})
	}
}

func TestXReadWithNothingNew(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "5-5", "foo", "bar")
	expectReply(t, conn, "$3\r\n5-5\r\n")

	// Nothing comes after these IDs: the reply is a null array.
	for _, id := range []string{"5-5", "5-6", "6-0", "100", "18446744073709551615-18446744073709551615"} {
		do(t, conn, "XREAD", "STREAMS", "stream_key", id)
		expectReply(t, conn, nullArrayReply)
	}
}

func TestXReadMissingStream(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XREAD", "STREAMS", "missing", "0-0")
	expectReply(t, conn, nullArrayReply)

	// Reading doesn't create the stream.
	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestXReadSeesNewEntries(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "1-1", "a", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "XREAD", "STREAMS", "stream_key", "1-1")
	expectReply(t, conn, nullArrayReply)

	do(t, conn, "XADD", "stream_key", "1-2", "b", "2")
	expectReply(t, conn, "$3\r\n1-2\r\n")
	do(t, conn, "XADD", "stream_key", "2-0", "c", "3", "d", "4")
	expectReply(t, conn, "$3\r\n2-0\r\n")

	// Reading again from the same ID returns what was added since, and
	// doesn't consume it: a second read gives the same result.
	want := array(streamReply("stream_key",
		entryReply("1-2", "b", "2"),
		entryReply("2-0", "c", "3", "d", "4"),
	))
	do(t, conn, "XREAD", "STREAMS", "stream_key", "1-1")
	expectReply(t, conn, want)
	do(t, conn, "XREAD", "STREAMS", "stream_key", "1-1")
	expectReply(t, conn, want)
}

func TestXReadKeywordIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	want := array(streamReply("stream_key", entryReply("0-1", "foo", "bar")))
	for _, words := range [][2]string{{"xread", "streams"}, {"XRead", "Streams"}, {"XREAD", "sTREAMS"}} {
		do(t, conn, words[0], words[1], "stream_key", "0-0")
		expectReply(t, conn, want)
	}
}

func TestXReadSyntaxErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "0-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n0-1\r\n")

	const wrongArity = "-ERR wrong number of arguments for 'xread' command\r\n"
	do(t, conn, "XREAD")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XREAD", "STREAMS")
	expectReply(t, conn, wrongArity)
	do(t, conn, "XREAD", "STREAMS", "stream_key")
	expectReply(t, conn, wrongArity)

	// The STREAMS keyword is mandatory.
	do(t, conn, "XREAD", "stream_key", "0-0", "extra")
	expectReply(t, conn, "-ERR syntax error\r\n")
	do(t, conn, "XREAD", "STREAM", "stream_key", "0-0")
	expectReply(t, conn, "-ERR syntax error\r\n")

	// Each key needs its ID.
	do(t, conn, "XREAD", "STREAMS", "stream_key", "other", "0-0")
	expectReply(t, conn, unbalancedXReadErr)

	for _, id := range []string{"abc", "", "1-", "1-2-3", "-", "1.5"} {
		do(t, conn, "XREAD", "STREAMS", "stream_key", id)
		expectReply(t, conn, invalidStreamIDError)
	}
}

func TestXReadAgainstOtherTypesIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "XREAD", "STREAMS", "text", "0-0")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "XREAD", "STREAMS", "list_key", "0-0")
	expectReply(t, conn, wrongTypeError)
}

func TestXReadKeepsFieldsInOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	fields := []string{"z", "1", "a", "2", "", "", "with space", "a\r\nb"}
	do(t, conn, append([]string{"XADD", "a key", "1-1"}, fields...)...)
	expectReply(t, conn, "$3\r\n1-1\r\n")

	do(t, conn, "XREAD", "STREAMS", "a key", "0-0")
	expectReply(t, conn, array(streamReply("a key", entryReply("1-1", fields...))))
}

func TestStreamAfter(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	st := &stream{}
	ids := []streamID{{1, 0}, {1, 1}, {2, 0}, {2, 5}, {3, 0}}
	for _, id := range ids {
		st.entries = append(st.entries, streamEntry{id: id})
	}

	tests := []struct {
		id   streamID
		want []streamID
	}{
		{streamID{0, 0}, ids},
		{streamID{0, maxUint64}, ids},
		{streamID{1, 0}, ids[1:]},
		{streamID{1, 1}, ids[2:]},
		{streamID{1, 7}, ids[2:]},
		{streamID{2, 5}, ids[4:]},
		{streamID{3, 0}, nil},
		{streamID{9, 9}, nil},
		{streamID{maxUint64, maxUint64}, nil},
	}
	for _, tt := range tests {
		var got []streamID
		for _, e := range st.after(tt.id) {
			got = append(got, e.id)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("after(%v) = %v, want %v", tt.id, got, tt.want)
		}
	}

	if got := (&stream{}).after(streamID{}); len(got) != 0 {
		t.Errorf("after on an empty stream = %v, want nothing", got)
	}
}
