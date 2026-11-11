package main

import (
	"errors"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestXAddAutoIDUsesTheCurrentTime(t *testing.T) {
	conn := dial(t, startTestServer(t))

	before := time.Now().UnixMilli()
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	id := readBulkString(t, conn)
	after := time.Now().UnixMilli()

	match := regexp.MustCompile(`^(\d+)-(\d+)$`).FindStringSubmatch(id)
	if match == nil {
		t.Fatalf("generated ID %q doesn't look like <milliseconds>-<sequence>", id)
	}
	// The time part is the current Unix time in milliseconds (not seconds),
	// and the first entry of a millisecond has sequence number 0.
	ms, _ := strconv.ParseInt(match[1], 10, 64)
	if ms < before || ms > after {
		t.Fatalf("time part of %q is not between %d and %d, the Unix time in milliseconds around the command", id, before, after)
	}
	if match[2] != "0" {
		t.Fatalf("sequence number of %q is not 0", id)
	}

	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestXAddAutoIDWithFakeClock(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	now := strconv.FormatInt(clock.Now().UnixMilli(), 10)

	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-0"))

	// More entries during the same millisecond: the sequence number grows.
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-1"))
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-2"))

	// A millisecond later the sequence number starts over.
	clock.Advance(time.Millisecond)
	later := strconv.FormatInt(clock.Now().UnixMilli(), 10)
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(later+"-0"))

	clock.Advance(3 * time.Second)
	muchLater := strconv.FormatInt(clock.Now().UnixMilli(), 10)
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(muchLater+"-0"))
}

func TestXAddAutoIDAfterEntryInTheFuture(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	future := strconv.FormatInt(clock.Now().UnixMilli()+60_000, 10)

	// An explicit ID that is ahead of the clock...
	do(t, conn, "XADD", "stream_key", future+"-5", "foo", "bar")
	expectReply(t, conn, bulk(future+"-5"))

	// ...forces generated IDs to continue from it rather than from the clock,
	// since IDs must keep increasing.
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(future+"-6"))
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(future+"-7"))
}

func TestXAddAutoIDAfterExplicitIDs(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	now := strconv.FormatInt(clock.Now().UnixMilli(), 10)

	// Entries from the past don't influence the generated ID.
	do(t, conn, "XADD", "stream_key", "1-1", "foo", "bar")
	expectReply(t, conn, "$3\r\n1-1\r\n")
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-0"))

	// Generated IDs count as the top of the stream for the other ID formats.
	do(t, conn, "XADD", "stream_key", "5-5", "foo", "bar")
	expectReply(t, conn, streamIDTooSmallError)
	do(t, conn, "XADD", "stream_key", now+"-*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-1"))
	do(t, conn, "XADD", "stream_key", now+"-9", "foo", "bar")
	expectReply(t, conn, bulk(now+"-9"))
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-10"))
}

func TestXAddAutoIDsAlwaysIncrease(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// With the real clock, many entries land in the same millisecond.
	last := streamID{}
	for i := 0; i < 200; i++ {
		do(t, conn, "XADD", "stream_key", "*", "n", strconv.Itoa(i))
		id, err := parseStreamID(readBulkString(t, conn), 0)
		if err != nil {
			t.Fatalf("entry %d: generated ID is not a valid ID: %v", i, err)
		}
		if id.compare(last) <= 0 {
			t.Fatalf("entry %d: generated ID %v doesn't come after the previous one, %v", i, id, last)
		}
		last = id
	}
}

func TestXAddAutoIDWhenClockGoesBackwards(t *testing.T) {
	clock := newFakeClock()
	srv := NewServer()
	srv.store.now = clock.Now
	conn := dial(t, startServer(t, srv))

	now := strconv.FormatInt(clock.Now().UnixMilli(), 10)
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-0"))

	// The clock is set back by an hour: IDs keep increasing regardless.
	clock.Advance(-time.Hour)
	do(t, conn, "XADD", "stream_key", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-1"))
}

func TestXAddAutoIDIsPerStream(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	now := strconv.FormatInt(clock.Now().UnixMilli(), 10)

	do(t, conn, "XADD", "first", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-0"))
	do(t, conn, "XADD", "first", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-1"))
	do(t, conn, "XADD", "second", "*", "foo", "bar")
	expectReply(t, conn, bulk(now+"-0"))
}

func TestXAddAutoIDStillValidatesTheRest(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "XADD", "stream_key", "*", "orphan-field")
	expectReply(t, conn, "-ERR wrong number of arguments for 'xadd' command\r\n")
	do(t, conn, "XADD", "stream_key", "*", "field", "value", "orphan")
	expectReply(t, conn, "-ERR wrong number of arguments for 'xadd' command\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+none\r\n")

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "XADD", "text", "*", "foo", "bar")
	expectReply(t, conn, wrongTypeError)

	for _, id := range []string{"**", "* ", " *", "*-0"} {
		do(t, conn, "XADD", "stream_key", id, "foo", "bar")
		expectReply(t, conn, invalidStreamIDError)
	}
}

func TestStreamIDNext(t *testing.T) {
	const maxUint64 = 1<<64 - 1

	tests := []struct {
		id     streamID
		want   streamID
		wantOK bool
	}{
		{streamID{0, 0}, streamID{0, 1}, true},
		{streamID{5, 7}, streamID{5, 8}, true},
		{streamID{5, maxUint64}, streamID{6, 0}, true},
		{streamID{maxUint64, 0}, streamID{maxUint64, 1}, true},
		{streamID{maxUint64, maxUint64}, streamID{}, false},
	}
	for _, tt := range tests {
		got, ok := tt.id.next()
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%v.next() = (%v, %v), want (%v, %v)", tt.id, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestStreamAddAutoID(t *testing.T) {
	const maxUint64 = 1<<64 - 1
	auto := newEntryID{auto: true}
	at := func(ms int64) time.Time { return time.UnixMilli(ms) }

	st := &stream{}
	steps := []struct {
		now  time.Time
		want streamID
	}{
		{at(1000), streamID{1000, 0}},
		{at(1000), streamID{1000, 1}},
		{at(1001), streamID{1001, 0}},
		{at(500), streamID{1001, 1}}, // the clock went backwards
		{at(2000), streamID{2000, 0}},
	}
	for i, step := range steps {
		got, err := st.add(auto, nil, step.now)
		if err != nil || got != step.want {
			t.Fatalf("step %d: add(*) = (%v, %v), want (%v, nil)", i, got, err, step.want)
		}
	}

	// When the sequence numbers of a millisecond are used up, the next
	// millisecond is used.
	st = &stream{lastID: streamID{1000, maxUint64}}
	if got, err := st.add(auto, nil, at(1000)); err != nil || got != (streamID{1001, 0}) {
		t.Fatalf("add(*) after 1000-max = (%v, %v), want (1001-0, nil)", got, err)
	}

	// And when there's no ID left at all, the entry is refused.
	st = &stream{lastID: streamID{maxUint64, maxUint64}}
	if _, err := st.add(auto, nil, at(1000)); !errors.Is(err, errStreamIDTooSmall) {
		t.Fatalf("add(*) on a stream whose IDs are exhausted: err = %v, want errStreamIDTooSmall", err)
	}
}
