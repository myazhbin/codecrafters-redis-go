package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// expectStrlen checks the length, in bytes, STRLEN reports for the key.
func expectStrlen(t *testing.T, conn net.Conn, key string, want int) {
	t.Helper()

	do(t, conn, "STRLEN", key)
	expectReply(t, conn, ":"+strconv.Itoa(want)+"\r\n")
}

func TestSetBitGrowsABitmap(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SETBIT", "bitmap_key", "1", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "STRLEN", "bitmap_key")
	expectReply(t, conn, ":1\r\n")

	// Offset 10 is in a second byte, which the string doesn't have yet.
	do(t, conn, "SETBIT", "bitmap_key", "10", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GETBIT", "bitmap_key", "10")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "STRLEN", "bitmap_key")
	expectReply(t, conn, ":2\r\n")

	// The string is 01000000 00100000.
	expectBitmap(t, srv, "bitmap_key", 0b01000000, 0b00100000)
	do(t, conn, "GET", "bitmap_key")
	expectReply(t, conn, bulk("@ "))
}

func TestSetBitGrowsTheStringUpToTheByteOfTheBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The length is the number of bytes it takes for the bit to exist.
	for _, tt := range []struct{ offset, length int }{
		{0, 1}, {7, 1}, {8, 2}, {10, 2}, {15, 2}, {16, 3}, {23, 3}, {24, 4}, {100, 13}, {1000, 126}, {65535, 8192}, {65536, 8193},
	} {
		key := "bitmap_" + strconv.Itoa(tt.offset)
		setBitTo(t, conn, key, tt.offset, 1, 0)
		expectStrlen(t, conn, key, tt.length)
		expectBit(t, conn, key, tt.offset, 1)
	}
}

func TestSetBitPadsTheStringWithZeros(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "bitmap_key", 39, 1, 0)
	expectBitmap(t, srv, "bitmap_key", 0, 0, 0, 0, 0b00000001)
	// Every bit before the one that was set is 0.
	for offset := range 39 {
		expectBit(t, conn, "bitmap_key", offset, 0)
	}
	do(t, conn, "GET", "bitmap_key")
	expectReply(t, conn, "$5\r\n\x00\x00\x00\x00\x01\r\n")
}

func TestSetBitGrowsAStringWithoutChangingWhatItHolds(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "mykey", "AB")
	expectReply(t, conn, "+OK\r\n")
	setBitTo(t, conn, "mykey", 31, 1, 0)

	expectBitmap(t, srv, "mykey", 'A', 'B', 0, 0b00000001)
	expectStrlen(t, conn, "mykey", 4)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("AB\x00\x01"))

	// A bitmap grows several times the same way.
	setBitTo(t, conn, "mykey", 32, 1, 0)
	setBitTo(t, conn, "mykey", 55, 1, 0)
	expectBitmap(t, srv, "mykey", 'A', 'B', 0, 0b00000001, 0b10000000, 0, 0b00000001)
}

func TestSetBitToZeroPastTheEndGrowsTheStringToo(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)
	// The bit is 0 already, but the string is made long enough to hold it.
	setBitTo(t, conn, "bitmap_key", 20, 0, 0)
	expectStrlen(t, conn, "bitmap_key", 3)
	expectBitmap(t, srv, "bitmap_key", 0b01000000, 0, 0)
}

func TestSetBitWithinTheStringDoesNotChangeItsLength(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 23, 1, 0)
	expectStrlen(t, conn, "bitmap_key", 3)
	for _, offset := range []int{0, 7, 8, 16, 22} {
		setBitTo(t, conn, "bitmap_key", offset, 1, 0)
		expectStrlen(t, conn, "bitmap_key", 3)
	}
	// Neither does clearing its last bits: a bitmap never shrinks.
	setBitTo(t, conn, "bitmap_key", 23, 0, 1)
	setBitTo(t, conn, "bitmap_key", 22, 0, 1)
	setBitTo(t, conn, "bitmap_key", 16, 0, 1)
	expectStrlen(t, conn, "bitmap_key", 3)
}

func TestGetBitDoesNotGrowTheString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)
	expectBit(t, conn, "bitmap_key", 1000, 0)
	expectStrlen(t, conn, "bitmap_key", 1)
	expectBit(t, conn, "missing_key", 1000, 0)
	expectStrlen(t, conn, "missing_key", 0)
}

func TestSetBitGrowsALargeBitmap(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A megabyte of zeros, then the bit.
	const offset = 8 * 1024 * 1024
	setBitTo(t, conn, "bitmap_key", offset, 1, 0)
	expectStrlen(t, conn, "bitmap_key", 1024*1024+1)
	expectBit(t, conn, "bitmap_key", offset, 1)
	expectBit(t, conn, "bitmap_key", offset-1, 0)
	expectBit(t, conn, "bitmap_key", offset+1, 0)
	expectBit(t, conn, "bitmap_key", 0, 0)

	// Bits can still be set anywhere in it.
	setBitTo(t, conn, "bitmap_key", 12345, 1, 0)
	setBitTo(t, conn, "bitmap_key", offset, 1, 1)
	expectBit(t, conn, "bitmap_key", 12345, 1)
	expectStrlen(t, conn, "bitmap_key", 1024*1024+1)
}

func TestGrowingABitmapIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	// Growing the string is a modification, even when the bit that is set
	// is 0 before and after.
	commands := [][]string{
		{"SETBIT", "bitmap_key", "1", "1"},
		{"SETBIT", "bitmap_key", "10", "1"},
		{"SETBIT", "bitmap_key", "30", "0"},
	}
	want := ""
	for i, command := range commands {
		if i == len(commands)-1 {
			do(t, watcher, "WATCH", "bitmap_key")
			expectReply(t, watcher, "+OK\r\n")
		}
		do(t, conn, command...)
		expectReply(t, conn, ":0\r\n")
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	do(t, watcher, "MULTI")
	expectReply(t, watcher, "+OK\r\n")
	do(t, watcher, "PING")
	expectReply(t, watcher, "+QUEUED\r\n")
	do(t, watcher, "EXEC")
	expectReply(t, watcher, "*-1\r\n")

	// A server that restarts rebuilds a bitmap of the same length.
	restarted := restartWithAOF(t, srv, args...)
	expectBitmap(t, restarted, "bitmap_key", 0b01000000, 0b00100000, 0, 0)
}

func TestStrlen(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The length is in bytes, not in bits or in characters.
	for _, tt := range []struct {
		value  string
		length int
	}{
		{"hello", 5},
		{"", 0},
		{"A", 1},
		{"hello world", 11},
		{"12345", 5},
		{"héllo ✓", 10},
		{"a\r\nb", 4},
		{"\x00\x00\x00", 3},
		{strings.Repeat("x", 100000), 100000},
	} {
		do(t, conn, "SET", "mykey", tt.value)
		expectReply(t, conn, "+OK\r\n")
		expectStrlen(t, conn, "mykey", tt.length)
	}

	// It follows what the other commands do to the string.
	do(t, conn, "SET", "counter", "99")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":100\r\n")
	expectStrlen(t, conn, "counter", 3)
}

func TestStrlenOfAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "mykey", "hello")
	expectReply(t, conn, "+OK\r\n")
	for _, key := range []string{"missing_key", "MYKEY", "mykey ", ""} {
		expectStrlen(t, conn, key, 0)
	}

	// Asking for a length creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("mykey"))
}

func TestStrlenOfAKeyThatExpired(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "mykey", "hello", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	expectStrlen(t, conn, "mykey", 5)
	clock.Advance(100 * time.Millisecond)
	expectStrlen(t, conn, "mykey", 0)
}

func TestStrlenOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))
	zadd(t, conn, "zset_key", "1", "a")

	for _, key := range []string{"list_key", "stream_key", "zset_key"} {
		do(t, conn, "STRLEN", key)
		expectReply(t, conn, wrongTypeError)
	}
}

func TestStrlenWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"STRLEN"}, {"STRLEN", "mykey", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'strlen' command\r\n")
	}
}

func TestStrlenCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "mykey", "hello")
	expectReply(t, conn, "+OK\r\n")
	for _, name := range []string{"STRLEN", "strlen", "StrLen"} {
		do(t, conn, name, "mykey")
		expectReply(t, conn, ":5\r\n")
	}
}

func TestStrlenInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"STRLEN", "bitmap_key"},
		{"SETBIT", "bitmap_key", "1", "1"},
		{"STRLEN", "bitmap_key"},
		{"SETBIT", "bitmap_key", "10", "1"},
		{"STRLEN", "bitmap_key"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n", ":0\r\n", ":1\r\n", ":0\r\n", ":2\r\n"))
}

func TestStrlenIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	setBitTo(t, conn, "bitmap_key", 10, 1, 0)
	expectPropagated(t, replica, "SETBIT", "bitmap_key", "10", "1")
	do(t, watcher, "WATCH", "bitmap_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectStrlen(t, conn, "bitmap_key", 2)
	expectStrlen(t, conn, "missing_key", 0)

	// Reading a length is neither logged nor propagated, and doesn't count
	// as a modification of the key.
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
}
