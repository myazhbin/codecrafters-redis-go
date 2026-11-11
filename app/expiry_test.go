package main

import (
	"testing"
	"time"
)

func TestSetWithPXExpiresKey(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "bar", "PX", "100")
	expectReply(t, conn, "+OK\r\n")

	// Right after SET the key is readable.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")

	// One millisecond before the deadline it's still there...
	clock.Advance(99 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")

	// ...and once the 100ms have elapsed it's gone, for good.
	clock.Advance(1 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")

	clock.Advance(time.Hour)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestSetWithEXExpiresKey(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "bar", "EX", "10")
	expectReply(t, conn, "+OK\r\n")

	// EX is in seconds: 10ms (or 9.999s) later the key is still there.
	clock.Advance(10 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")

	clock.Advance(9989 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")

	clock.Advance(1 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

// TestSetWithPXExpiresKeyInRealTime doesn't fake the clock: it checks that the
// server is wired to the real one, the way the CodeCrafters tester does.
func TestSetWithPXExpiresKeyInRealTime(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "bar", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")

	time.Sleep(200 * time.Millisecond)

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestSetWithoutExpiryNeverExpires(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")

	clock.Advance(200 * 365 * 24 * time.Hour)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
}

func TestSetExpiryOptionsAreCaseInsensitive(t *testing.T) {
	for _, option := range []string{"PX", "px", "pX", "Px"} {
		t.Run(option, func(t *testing.T) {
			addr, clock := startTestServerWithClock(t)
			conn := dial(t, addr)

			do(t, conn, "set", "foo", "bar", option, "100")
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "GET", "foo")
			expectReply(t, conn, "$3\r\nbar\r\n")

			clock.Advance(100 * time.Millisecond)
			do(t, conn, "GET", "foo")
			expectReply(t, conn, "$-1\r\n")
		})
	}

	for _, option := range []string{"EX", "ex", "eX", "Ex"} {
		t.Run(option, func(t *testing.T) {
			addr, clock := startTestServerWithClock(t)
			conn := dial(t, addr)

			do(t, conn, "set", "foo", "bar", option, "1")
			expectReply(t, conn, "+OK\r\n")

			clock.Advance(999 * time.Millisecond)
			do(t, conn, "GET", "foo")
			expectReply(t, conn, "$3\r\nbar\r\n")

			clock.Advance(1 * time.Millisecond)
			do(t, conn, "GET", "foo")
			expectReply(t, conn, "$-1\r\n")
		})
	}
}

func TestSetReplacesExpiry(t *testing.T) {
	t.Run("plain SET removes the expiry", func(t *testing.T) {
		addr, clock := startTestServerWithClock(t)
		conn := dial(t, addr)

		do(t, conn, "SET", "foo", "old", "PX", "100")
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, "SET", "foo", "new")
		expectReply(t, conn, "+OK\r\n")

		clock.Advance(time.Second)
		do(t, conn, "GET", "foo")
		expectReply(t, conn, "$3\r\nnew\r\n")
	})

	t.Run("SET with PX restarts the countdown", func(t *testing.T) {
		addr, clock := startTestServerWithClock(t)
		conn := dial(t, addr)

		do(t, conn, "SET", "foo", "old", "PX", "100")
		expectReply(t, conn, "+OK\r\n")

		clock.Advance(80 * time.Millisecond)
		do(t, conn, "SET", "foo", "new", "PX", "100")
		expectReply(t, conn, "+OK\r\n")

		// 160ms after the first SET, but only 80ms after the second.
		clock.Advance(80 * time.Millisecond)
		do(t, conn, "GET", "foo")
		expectReply(t, conn, "$3\r\nnew\r\n")

		clock.Advance(20 * time.Millisecond)
		do(t, conn, "GET", "foo")
		expectReply(t, conn, "$-1\r\n")
	})

	t.Run("an expired key can be set again", func(t *testing.T) {
		addr, clock := startTestServerWithClock(t)
		conn := dial(t, addr)

		do(t, conn, "SET", "foo", "old", "PX", "100")
		expectReply(t, conn, "+OK\r\n")
		clock.Advance(time.Second)

		do(t, conn, "SET", "foo", "new")
		expectReply(t, conn, "+OK\r\n")
		clock.Advance(time.Second)
		do(t, conn, "GET", "foo")
		expectReply(t, conn, "$3\r\nnew\r\n")
	})
}

func TestExpiryIsPerKey(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "short", "a", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "long", "b", "PX", "500")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "forever", "c")
	expectReply(t, conn, "+OK\r\n")

	clock.Advance(200 * time.Millisecond)
	do(t, conn, "GET", "short")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "long")
	expectReply(t, conn, "$1\r\nb\r\n")
	do(t, conn, "GET", "forever")
	expectReply(t, conn, "$1\r\nc\r\n")

	clock.Advance(300 * time.Millisecond)
	do(t, conn, "GET", "long")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "forever")
	expectReply(t, conn, "$1\r\nc\r\n")
}

func TestSetInvalidOptions(t *testing.T) {
	const (
		syntaxError   = "-ERR syntax error\r\n"
		notAnInteger  = "-ERR value is not an integer or out of range\r\n"
		invalidExpire = "-ERR invalid expire time in 'set' command\r\n"
	)

	tests := []struct {
		name    string
		options []string
		want    string
	}{
		{"unknown option", []string{"FOO"}, syntaxError},
		{"PX without a value", []string{"PX"}, syntaxError},
		{"EX without a value", []string{"EX"}, syntaxError},
		{"both EX and PX", []string{"EX", "10", "PX", "100"}, syntaxError},
		{"PX twice", []string{"PX", "100", "PX", "100"}, syntaxError},
		{"trailing garbage", []string{"PX", "100", "garbage"}, syntaxError},
		{"syntax errors win over value errors", []string{"PX", "abc", "garbage"}, syntaxError},
		{"non numeric PX", []string{"PX", "abc"}, notAnInteger},
		{"fractional PX", []string{"PX", "1.5"}, notAnInteger},
		{"PX larger than an int64", []string{"PX", "9223372036854775808"}, notAnInteger},
		{"PX with a plus sign", []string{"PX", "+100"}, notAnInteger},
		{"PX with leading zeros", []string{"PX", "0100"}, notAnInteger},
		{"EX with surrounding spaces", []string{"EX", " 10 "}, notAnInteger},
		{"zero PX", []string{"PX", "0"}, invalidExpire},
		{"negative PX", []string{"PX", "-100"}, invalidExpire},
		{"zero EX", []string{"EX", "0"}, invalidExpire},
		{"negative EX", []string{"EX", "-1"}, invalidExpire},
		{"EX overflowing once converted to milliseconds", []string{"EX", "9223372036854775807"}, invalidExpire},
		{"PX overflowing once added to the current time", []string{"PX", "9223372036854775807"}, invalidExpire},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dial(t, startTestServer(t))

			do(t, conn, "SET", "existing", "untouched")
			expectReply(t, conn, "+OK\r\n")

			do(t, conn, append([]string{"SET", "existing", "changed"}, tt.options...)...)
			expectReply(t, conn, tt.want)
			do(t, conn, append([]string{"SET", "new", "value"}, tt.options...)...)
			expectReply(t, conn, tt.want)

			// A rejected SET changes nothing.
			do(t, conn, "GET", "existing")
			expectReply(t, conn, "$9\r\nuntouched\r\n")
			do(t, conn, "GET", "new")
			expectReply(t, conn, "$-1\r\n")
		})
	}
}

func TestSetWithVeryLongExpiry(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	// About 31,000 years: too long for a time.Duration, but valid for Redis.
	do(t, conn, "SET", "foo", "bar", "PX", "1000000000000000")
	expectReply(t, conn, "+OK\r\n")

	clock.Advance(200 * 365 * 24 * time.Hour)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
}

func TestStoreExpiry(t *testing.T) {
	clock := newFakeClock()
	st := newStore()
	st.now = clock.Now

	st.setString("temp", "value", clock.Now().Add(100*time.Millisecond))
	st.setString("keep", "value", noExpiry)

	if _, ok := st.lookup("temp"); !ok {
		t.Fatalf("key is missing before its expiry time")
	}

	clock.Advance(100 * time.Millisecond)
	if value, ok, _ := st.getString("temp"); ok {
		t.Fatalf("expired key is still readable: %q", value)
	}
	// Reading an expired key also frees it.
	if _, present := st.entries["temp"]; present {
		t.Fatalf("expired key was not deleted from the keyspace on access")
	}

	clock.Advance(24 * time.Hour)
	if _, ok := st.lookup("keep"); !ok {
		t.Fatalf("key without expiry disappeared")
	}
}
