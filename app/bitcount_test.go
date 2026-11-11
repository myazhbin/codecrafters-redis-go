package main

import (
	"math/rand"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// expectBitCount checks the number of set bits BITCOUNT reports for the key,
// given the arguments that follow the key, if any.
func expectBitCount(t *testing.T, conn net.Conn, key string, want int, arguments ...string) {
	t.Helper()

	do(t, conn, append([]string{"BITCOUNT", key}, arguments...)...)
	expectReply(t, conn, ":"+strconv.Itoa(want)+"\r\n")
}

func TestBitCount(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The string is 01000000 00100000.
	do(t, conn, "SETBIT", "bitmap_key", "1", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SETBIT", "bitmap_key", "10", "1")
	expectReply(t, conn, ":0\r\n")

	// Without a range, the bits of the whole string are counted.
	do(t, conn, "BITCOUNT", "bitmap_key")
	expectReply(t, conn, ":2\r\n")
	// The range is made of bytes, not bits, and includes both of its ends.
	do(t, conn, "BITCOUNT", "bitmap_key", "0", "1")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "BITCOUNT", "bitmap_key", "0", "0")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "BITCOUNT", "bitmap_key", "1", "1")
	expectReply(t, conn, ":1\r\n")
}

func TestBitCountOfAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	expectBitCount(t, conn, "missing_key", 0)
	expectBitCount(t, conn, "missing_key", 0, "0", "0")
	expectBitCount(t, conn, "missing_key", 0, "0", "100")
	expectBitCount(t, conn, "missing_key", 0, "0", "-1")
	expectBitCount(t, conn, "missing_key", 0, "0", "-1", "BIT")

	// Counting bits creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestBitCountRangesBeyondTheString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)
	setBitTo(t, conn, "bitmap_key", 10, 1, 0)

	// A start that is past the end of the string leaves nothing to count.
	expectBitCount(t, conn, "bitmap_key", 0, "2", "2")
	expectBitCount(t, conn, "bitmap_key", 0, "2", "5")
	expectBitCount(t, conn, "bitmap_key", 0, "100", "200")
	// An end that is past it is the same as the last byte.
	expectBitCount(t, conn, "bitmap_key", 2, "0", "2")
	expectBitCount(t, conn, "bitmap_key", 2, "0", "100")
	expectBitCount(t, conn, "bitmap_key", 1, "1", "100")
	expectBitCount(t, conn, "bitmap_key", 1, "1", "9223372036854775807")
	// A start that comes after the end is an empty range.
	expectBitCount(t, conn, "bitmap_key", 0, "1", "0")
	expectBitCount(t, conn, "bitmap_key", 0, "100", "0")
}

func TestBitCountOfAString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The example of the Redis documentation.
	do(t, conn, "SET", "mykey", "foobar")
	expectReply(t, conn, "+OK\r\n")
	expectBitCount(t, conn, "mykey", 26)
	expectBitCount(t, conn, "mykey", 4, "0", "0")
	expectBitCount(t, conn, "mykey", 6, "1", "1")
	expectBitCount(t, conn, "mykey", 6, "1", "1", "BYTE")
	expectBitCount(t, conn, "mykey", 17, "5", "30", "BIT")

	for _, tt := range []struct {
		value string
		want  int
	}{
		{"", 0},
		{"\x00", 0},
		{"\x00\x00\x00", 0},
		{"\xff", 8},
		{"\xff\xff\xff", 24},
		{"A", 2},
		{"\x80", 1},
		{"\x01", 1},
		{"12345", 17},
		{strings.Repeat("\xff", 1000), 8000},
		{strings.Repeat("\x0f\xf0\x01", 333), 9 * 333},
	} {
		do(t, conn, "SET", "mykey", tt.value)
		expectReply(t, conn, "+OK\r\n")
		expectBitCount(t, conn, "mykey", tt.want)
		expectBitCount(t, conn, "mykey", tt.want, "0", strconv.Itoa(len(tt.value)))
	}
}

func TestBitCountFollowsTheChangesOfTheBitmap(t *testing.T) {
	conn := dial(t, startTestServer(t))

	expectBitCount(t, conn, "bitmap_key", 0)
	for count, offset := range []int{3, 12, 4, 100, 0} {
		setBitTo(t, conn, "bitmap_key", offset, 1, 0)
		expectBitCount(t, conn, "bitmap_key", count+1)
	}
	// Setting a bit that is set already adds nothing.
	setBitTo(t, conn, "bitmap_key", 12, 1, 1)
	expectBitCount(t, conn, "bitmap_key", 5)
	// Growing the bitmap with a 0 doesn't either.
	setBitTo(t, conn, "bitmap_key", 500, 0, 0)
	expectBitCount(t, conn, "bitmap_key", 5)

	setBitTo(t, conn, "bitmap_key", 100, 0, 1)
	setBitTo(t, conn, "bitmap_key", 3, 0, 1)
	expectBitCount(t, conn, "bitmap_key", 3)
	expectBitCount(t, conn, "bitmap_key", 2, "0", "0")
	expectBitCount(t, conn, "bitmap_key", 1, "1", "1")
	expectBitCount(t, conn, "bitmap_key", 0, "2", "100")
}

func TestBitCountWithNegativeIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "foobar": f has 4 bits set, o 6, b 3, a 3 and r 4.
	do(t, conn, "SET", "mykey", "foobar")
	expectReply(t, conn, "+OK\r\n")

	// -1 is the last byte, -2 the one before, and so on.
	expectBitCount(t, conn, "mykey", 26, "0", "-1")
	expectBitCount(t, conn, "mykey", 4, "-1", "-1")
	expectBitCount(t, conn, "mykey", 7, "-2", "-1")
	expectBitCount(t, conn, "mykey", 22, "0", "-2")
	expectBitCount(t, conn, "mykey", 18, "1", "-2")
	expectBitCount(t, conn, "mykey", 26, "-6", "-1")
	expectBitCount(t, conn, "mykey", 3, "-3", "3")
	// An index that points before the first byte is the same as 0, whichever
	// end of the range it is.
	expectBitCount(t, conn, "mykey", 26, "-100", "-1")
	expectBitCount(t, conn, "mykey", 4, "-100", "0")
	expectBitCount(t, conn, "mykey", 4, "0", "-100")
	// Unless both indexes count from the end and are the wrong way around.
	expectBitCount(t, conn, "mykey", 0, "-1", "-2")
	expectBitCount(t, conn, "mykey", 0, "-1", "-100")
	expectBitCount(t, conn, "mykey", 0, "-1", "0")
}

func TestBitCountWithBitIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// 01000001 01000010
	do(t, conn, "SET", "mykey", "AB")
	expectReply(t, conn, "+OK\r\n")

	expectBitCount(t, conn, "mykey", 4, "0", "-1", "BIT")
	expectBitCount(t, conn, "mykey", 4, "0", "15", "bit")
	expectBitCount(t, conn, "mykey", 0, "0", "0", "BIT")
	expectBitCount(t, conn, "mykey", 1, "1", "1", "BIT")
	expectBitCount(t, conn, "mykey", 1, "0", "6", "BIT")
	expectBitCount(t, conn, "mykey", 2, "0", "7", "BIT")
	expectBitCount(t, conn, "mykey", 2, "7", "9", "BIT")
	expectBitCount(t, conn, "mykey", 3, "2", "14", "BIT")
	expectBitCount(t, conn, "mykey", 1, "-2", "-1", "BIT")
	expectBitCount(t, conn, "mykey", 0, "-1", "-1", "BIT")
	expectBitCount(t, conn, "mykey", 4, "0", "1000", "BIT")
	expectBitCount(t, conn, "mykey", 0, "16", "20", "BIT")
	expectBitCount(t, conn, "mykey", 0, "9", "8", "BIT")
	// BYTE is what the indexes are without it.
	expectBitCount(t, conn, "mykey", 2, "0", "0", "BYTE")
	expectBitCount(t, conn, "mykey", 2, "1", "1", "byte")
}

// TestBitCountMatchesModel compares BITCOUNT with a deliberately naive
// implementation of its rules, which looks at the bits one by one, for every
// combination of small indexes, in bytes and in bits.
func TestBitCountMatchesModel(t *testing.T) {
	conn := dial(t, startTestServer(t))

	model := func(value string, start, end int, inBits bool) int {
		total := len(value)
		if inBits {
			total *= 8
		}
		if start < 0 && end < 0 && start > end {
			return 0
		}
		if start < 0 {
			start = max(start+total, 0)
		}
		if end < 0 {
			end = max(end+total, 0)
		}
		end = min(end, total-1)

		count := 0
		for offset := 0; offset < 8*len(value); offset++ {
			index := offset
			if !inBits {
				index = offset / 8
			}
			if index >= start && index <= end && value[offset/8]&(0x80>>(offset%8)) != 0 {
				count++
			}
		}
		return count
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for size := 0; size <= 3; size++ {
		raw := make([]byte, size)
		rng.Read(raw)
		value := string(raw)
		do(t, conn, "SET", "mykey", value)
		expectReply(t, conn, "+OK\r\n")

		for start := -size - 2; start <= size+2; start++ {
			for end := -size - 2; end <= size+2; end++ {
				expectBitCount(t, conn, "mykey", model(value, start, end, false), strconv.Itoa(start), strconv.Itoa(end))
			}
		}
		for start := -8*size - 2; start <= 8*size+2; start++ {
			for end := -8*size - 2; end <= 8*size+2; end++ {
				expectBitCount(t, conn, "mykey", model(value, start, end, true), strconv.Itoa(start), strconv.Itoa(end), "BIT")
			}
		}
	}

	// Longer strings, with ranges picked at random.
	for range 20 {
		raw := make([]byte, 1+rng.Intn(200))
		rng.Read(raw)
		value := string(raw)
		do(t, conn, "SET", "mykey", value)
		expectReply(t, conn, "+OK\r\n")
		expectBitCount(t, conn, "mykey", model(value, 0, -1, false))
		for range 20 {
			start, end := rng.Intn(2*len(value))-len(value), rng.Intn(2*len(value))-len(value)
			expectBitCount(t, conn, "mykey", model(value, start, end, false), strconv.Itoa(start), strconv.Itoa(end))
			start, end = rng.Intn(16*len(value))-8*len(value), rng.Intn(16*len(value))-8*len(value)
			expectBitCount(t, conn, "mykey", model(value, start, end, true), strconv.Itoa(start), strconv.Itoa(end), "BIT")
		}
	}
}

func TestBitCountOfALargeBitmap(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const megabyte = 1024 * 1024
	setBitTo(t, conn, "bitmap_key", 8*megabyte, 1, 0)
	expectBitCount(t, conn, "bitmap_key", 1)
	expectBitCount(t, conn, "bitmap_key", 0, "0", strconv.Itoa(megabyte-1))
	expectBitCount(t, conn, "bitmap_key", 1, strconv.Itoa(megabyte), strconv.Itoa(megabyte))

	for i := range 50 {
		setBitTo(t, conn, "bitmap_key", 1000*i+7, 1, 0)
	}
	expectBitCount(t, conn, "bitmap_key", 51)
	expectBitCount(t, conn, "bitmap_key", 50, "0", "-2")
}

func TestBitCountInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)

	// The indexes are checked whether the key exists or not.
	for _, key := range []string{"bitmap_key", "missing_key"} {
		for _, indexes := range [][2]string{{"a", "1"}, {"0", "b"}, {"", "1"}, {"0", ""}, {"1.5", "2"}, {"0", "2.0"}, {" 0", "1"}, {"0", "9223372036854775808"}} {
			do(t, conn, "BITCOUNT", key, indexes[0], indexes[1])
			expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
		}
		// A start comes with an end, and what follows them is a unit.
		do(t, conn, "BITCOUNT", key, "0")
		expectReply(t, conn, "-ERR syntax error\r\n")
		do(t, conn, "BITCOUNT", key, "0", "1", "BITS")
		expectReply(t, conn, "-ERR syntax error\r\n")
		do(t, conn, "BITCOUNT", key, "0", "1", "BIT", "extra")
		expectReply(t, conn, "-ERR syntax error\r\n")
	}

	do(t, conn, "BITCOUNT")
	expectReply(t, conn, "-ERR wrong number of arguments for 'bitcount' command\r\n")
}

func TestBitCountOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))
	zadd(t, conn, "zset_key", "1", "a")

	for _, key := range []string{"list_key", "stream_key", "zset_key"} {
		do(t, conn, "BITCOUNT", key)
		expectReply(t, conn, wrongTypeError)
		do(t, conn, "BITCOUNT", key, "0", "1")
		expectReply(t, conn, wrongTypeError)
		// The arguments are checked first.
		do(t, conn, "BITCOUNT", key, "0", "x")
		expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
	}
}

func TestBitCountCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)
	setBitTo(t, conn, "bitmap_key", 10, 1, 0)
	for _, name := range []string{"BITCOUNT", "bitcount", "BitCount"} {
		do(t, conn, name, "bitmap_key")
		expectReply(t, conn, ":2\r\n")
	}
}

func TestBitCountInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"BITCOUNT", "bitmap_key"},
		{"SETBIT", "bitmap_key", "1", "1"},
		{"BITCOUNT", "bitmap_key"},
		{"SETBIT", "bitmap_key", "10", "1"},
		{"BITCOUNT", "bitmap_key"},
		{"BITCOUNT", "bitmap_key", "1", "1"},
		{"BITCOUNT", "bitmap_key", "1"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n", ":0\r\n", ":1\r\n", ":0\r\n", ":2\r\n", ":1\r\n", "-ERR syntax error\r\n"))
}

func TestBitCountIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	setBitTo(t, conn, "bitmap_key", 10, 1, 0)
	expectPropagated(t, replica, "SETBIT", "bitmap_key", "10", "1")
	do(t, watcher, "WATCH", "bitmap_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectBitCount(t, conn, "bitmap_key", 1)
	expectBitCount(t, conn, "bitmap_key", 1, "0", "100")
	expectBitCount(t, conn, "missing_key", 0)

	// Counting bits is neither logged nor propagated, doesn't count as a
	// modification of the key, and leaves the string as long as it was.
	if got, want := readFile(t, incrPath), encodeCommand("SETBIT", "bitmap_key", "10", "1"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	do(t, watcher, "MULTI")
	expectReply(t, watcher, "+OK\r\n")
	do(t, watcher, "PING")
	expectReply(t, watcher, "+QUEUED\r\n")
	do(t, watcher, "EXEC")
	expectReply(t, watcher, array("+PONG\r\n"))
	expectBitmap(t, srv, "bitmap_key", 0, 0b00100000)
}
