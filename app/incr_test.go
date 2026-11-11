package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestIncrExistingNumber(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, "+OK\r\n")

	// The reply is the new value, as a RESP integer.
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":42\r\n")
}

func TestIncrSeveralTimes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "5")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":6\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":7\r\n")

	// The new value is stored: it's what GET returns, as a string.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n7\r\n")
	do(t, conn, "TYPE", "foo")
	expectReply(t, conn, "+string\r\n")
}

func TestIncrNumbers(t *testing.T) {
	tests := []struct {
		name, initial, want string
	}{
		{"zero", "0", "1"},
		{"gains a digit", "9", "10"},
		{"negative number", "-10", "-9"},
		{"minus one becomes zero", "-1", "0"},
		{"large number", "1526919030474", "1526919030475"},
		{"up to the largest 64-bit integer", "9223372036854775806", "9223372036854775807"},
		{"smallest 64-bit integer", "-9223372036854775808", "-9223372036854775807"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dial(t, startTestServer(t))

			do(t, conn, "SET", "foo", tt.initial)
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "INCR", "foo")
			expectReply(t, conn, ":"+tt.want+"\r\n")
			do(t, conn, "GET", "foo")
			expectReply(t, conn, bulk(tt.want))
		})
	}
}

func TestIncrOverflow(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "9223372036854775807")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, "-ERR increment or decrement would overflow\r\n")

	// The value is unchanged.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$19\r\n9223372036854775807\r\n")
}

func TestIncrOnlyAffectsItsKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "first", "10")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "second", "20")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "INCR", "first")
	expectReply(t, conn, ":11\r\n")
	do(t, conn, "GET", "second")
	expectReply(t, conn, "$2\r\n20\r\n")
}

func TestIncrKeepsExpiry(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1", "PX", "100")
	expectReply(t, conn, "+OK\r\n")

	clock.Advance(60 * time.Millisecond)
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":2\r\n")

	// Incrementing neither removed nor restarted the expiry.
	clock.Advance(39 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n2\r\n")
	clock.Advance(1 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestIncrThenSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "41")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":42\r\n")

	// SET starts over from whatever it's given.
	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":101\r\n")
}

func TestIncrIsSharedBetweenClients(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "SET", "counter", "0")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "INCR", "counter")
	expectReply(t, first, ":1\r\n")
	do(t, second, "INCR", "counter")
	expectReply(t, second, ":2\r\n")
	do(t, first, "INCR", "counter")
	expectReply(t, first, ":3\r\n")
}

func TestIncrIsAtomic(t *testing.T) {
	addr := startTestServer(t)

	const clients, incrementsPerClient = 10, 50

	setup := dial(t, addr)
	do(t, setup, "SET", "counter", "0")
	expectReply(t, setup, "+OK\r\n")

	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(id int, conn net.Conn) {
			defer wg.Done()
			reader := bufio.NewReader(conn)
			for n := 0; n < incrementsPerClient; n++ {
				if _, err := conn.Write([]byte(encodeCommand("INCR", "counter"))); err != nil {
					errs <- fmt.Errorf("client %d: write: %w", id, err)
					return
				}
				if _, err := reader.ReadString('\n'); err != nil {
					errs <- fmt.Errorf("client %d: read: %w", id, err)
					return
				}
			}
		}(i, conn)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// No increment was lost to a race between two clients.
	do(t, setup, "GET", "counter")
	expectReply(t, setup, bulk(strconv.Itoa(clients*incrementsPerClient)))
}

func TestIncrMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A key that doesn't exist counts as 0: its value becomes 1.
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "INCR", "bar")
	expectReply(t, conn, ":1\r\n")
}

func TestIncrMissingKeyCreatesAString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "INCR", "missing_key")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "GET", "missing_key")
	expectReply(t, conn, "$1\r\n1\r\n")
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+string\r\n")
}

func TestIncrMissingKeyThenKeepsCounting(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for want := 1; want <= 12; want++ {
		do(t, conn, "INCR", "counter")
		expectReply(t, conn, ":"+strconv.Itoa(want)+"\r\n")
	}
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$2\r\n12\r\n")
}

func TestIncrCreatedKeyNeverExpires(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":1\r\n")

	clock.Advance(100 * 365 * 24 * time.Hour)
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$1\r\n1\r\n")
}

func TestIncrExpiredKeyStartsOver(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "counter", "41", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	clock.Advance(100 * time.Millisecond)

	// The old value is gone, and so is its expiry.
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":1\r\n")
	clock.Advance(time.Hour)
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$1\r\n1\r\n")
}

func TestIncrKeyOfEmptiedListStartsOver(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "key", "only")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LPOP", "key")
	expectReply(t, conn, "$4\r\nonly\r\n")

	// The list is gone, so the key is free to become a counter.
	do(t, conn, "INCR", "key")
	expectReply(t, conn, ":1\r\n")
}

func TestIncrMissingKeysAreIndependent(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "INCR", "first")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "INCR", "first")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "INCR", "second")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "INCR", randomWord(t))
	expectReply(t, conn, ":1\r\n")
}

func TestIncrMissingKeyIsAtomic(t *testing.T) {
	addr := startTestServer(t)

	const clients = 20
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	// Everybody increments a key that doesn't exist yet at the same time:
	// exactly one of them creates it, and every reply is a different number.
	replies := make(chan string, clients)
	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func(conn net.Conn) {
			defer wg.Done()
			conn.Write([]byte(encodeCommand("INCR", "fresh")))
			line, _ := bufio.NewReader(conn).ReadString('\n')
			replies <- line
		}(conn)
	}
	wg.Wait()
	close(replies)

	seen := make(map[string]bool)
	for reply := range replies {
		if seen[reply] {
			t.Fatalf("two clients got the same reply %q", reply)
		}
		seen[reply] = true
	}
	for want := 1; want <= clients; want++ {
		if reply := ":" + strconv.Itoa(want) + "\r\n"; !seen[reply] {
			t.Fatalf("no client got the reply %q", reply)
		}
	}
}

const notAnIntegerError = "-ERR value is not an integer or out of range\r\n"

func TestIncrNonNumericValue(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "xyz")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, notAnIntegerError)

	// The value is left as it was.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nxyz\r\n")
}

func TestIncrValuesThatAreNotIntegers(t *testing.T) {
	values := []struct {
		name, value string
	}{
		{"text", "xyz"},
		{"empty string", ""},
		{"decimal number", "3.14"},
		{"integer written as a decimal", "5.0"},
		{"scientific notation", "1e3"},
		{"hexadecimal", "0x10"},
		{"number followed by text", "12abc"},
		{"text followed by a number", "abc12"},
		{"leading space", " 5"},
		{"trailing space", "5 "},
		{"trailing newline", "5\n"},
		{"explicit plus sign", "+5"},
		{"leading zeros", "005"},
		{"minus zero", "-0"},
		{"lone minus sign", "-"},
		{"one above the largest 64-bit integer", "9223372036854775808"},
		{"one below the smallest 64-bit integer", "-9223372036854775809"},
		{"far too large", "99999999999999999999999999"},
		{"thousands separators", "1,000"},
		{"binary data", "\x00\x01"},
	}

	for _, tt := range values {
		t.Run(tt.name, func(t *testing.T) {
			conn := dial(t, startTestServer(t))

			do(t, conn, "SET", "foo", tt.value)
			expectReply(t, conn, "+OK\r\n")
			do(t, conn, "INCR", "foo")
			expectReply(t, conn, notAnIntegerError)
			do(t, conn, "GET", "foo")
			expectReply(t, conn, bulk(tt.value))
		})
	}
}

func TestIncrNonNumericValueKeepsExpiry(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "xyz", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, notAnIntegerError)

	// A failed INCR changes nothing, the expiry included.
	clock.Advance(99 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nxyz\r\n")
	clock.Advance(1 * time.Millisecond)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestIncrWorksAgainOnceTheValueIsNumeric(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "xyz")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, notAnIntegerError)

	do(t, conn, "SET", "foo", "10")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, ":11\r\n")
}

func TestIncrAgainstOtherTypesIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "n", "1")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	// Not being a string at all is a different error from not being numeric.
	do(t, conn, "INCR", "list_key")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "INCR", "stream_key")
	expectReply(t, conn, wrongTypeError)

	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("1"))
}

func TestIncrErrorDoesNotAffectOtherKeys(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "xyz")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "number", "1")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "INCR", "text")
	expectReply(t, conn, notAnIntegerError)
	do(t, conn, "INCR", "number")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "INCR", "missing")
	expectReply(t, conn, ":1\r\n")
}

func TestIncrWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "INCR")
	expectReply(t, conn, "-ERR wrong number of arguments for 'incr' command\r\n")
	do(t, conn, "INCR", "foo", "2")
	expectReply(t, conn, "-ERR wrong number of arguments for 'incr' command\r\n")
}

func TestIncrIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "0")
	expectReply(t, conn, "+OK\r\n")
	for i, name := range []string{"incr", "Incr", "iNCR"} {
		do(t, conn, name, "foo")
		expectReply(t, conn, ":"+strconv.Itoa(i+1)+"\r\n")
	}
}

func TestParseInt(t *testing.T) {
	valid := map[string]int64{
		"0":                    0,
		"7":                    7,
		"-7":                   -7,
		"42":                   42,
		"1000":                 1000,
		"9223372036854775807":  9223372036854775807,
		"-9223372036854775808": -9223372036854775808,
	}
	for input, want := range valid {
		if got, ok := parseInt(input); !ok || got != want {
			t.Errorf("parseInt(%q) = (%d, %v), want (%d, true)", input, got, ok, want)
		}
	}

	// Anything but the canonical decimal representation is refused.
	for _, input := range []string{
		"", " ", "abc", "1a", "a1", "1.0", "1e3", "0x10", "+1", "-", "--1", "-0", "00", "007", "-07",
		" 1", "1 ", "1\n", "1_000", "9223372036854775808", "-9223372036854775809", "٣",
	} {
		if got, ok := parseInt(input); ok {
			t.Errorf("parseInt(%q) = (%d, true), want it to be refused", input, got)
		}
	}
}
