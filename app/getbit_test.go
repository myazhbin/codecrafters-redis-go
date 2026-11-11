package main

import (
	"math/rand"
	"net"
	"strconv"
	"testing"
	"time"
)

// expectBit checks the bit GETBIT reports at the offset.
func expectBit(t *testing.T, conn net.Conn, key string, offset, want int) {
	t.Helper()

	do(t, conn, "GETBIT", key, strconv.Itoa(offset))
	expectReply(t, conn, ":"+strconv.Itoa(want)+"\r\n")
}

func TestGetBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SETBIT", "bit_key", "2", "1")
	expectReply(t, conn, ":0\r\n")

	// A bit that was set.
	do(t, conn, "GETBIT", "bit_key", "2")
	expectReply(t, conn, ":1\r\n")
	// A bit that wasn't.
	do(t, conn, "GETBIT", "bit_key", "3")
	expectReply(t, conn, ":0\r\n")
	// A key that doesn't exist is a bitmap that is all zeros.
	do(t, conn, "GETBIT", "new_key", "2")
	expectReply(t, conn, ":0\r\n")
}

func TestGetBitOfEachBitOfAByte(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// newsletter_subscribers: 10001010
	for _, offset := range []int{0, 4, 6} {
		setBitTo(t, conn, "newsletter_subscribers", offset, 1, 0)
	}
	for offset, want := range []int{1, 0, 0, 0, 1, 0, 1, 0} {
		expectBit(t, conn, "newsletter_subscribers", offset, want)
	}
}

func TestGetBitFollowsSetBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	expectBit(t, conn, "bit_key", 5, 0)
	setBitTo(t, conn, "bit_key", 5, 1, 0)
	expectBit(t, conn, "bit_key", 5, 1)
	// The neighbours of the bit are untouched.
	expectBit(t, conn, "bit_key", 4, 0)
	expectBit(t, conn, "bit_key", 6, 0)

	setBitTo(t, conn, "bit_key", 5, 0, 1)
	expectBit(t, conn, "bit_key", 5, 0)
	setBitTo(t, conn, "bit_key", 5, 1, 0)
	setBitTo(t, conn, "bit_key", 4, 1, 0)
	expectBit(t, conn, "bit_key", 5, 1)
	expectBit(t, conn, "bit_key", 4, 1)
}

func TestGetBitBeyondTheEndOfTheString(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "bit_key", 7, 1, 0)
	expectBit(t, conn, "bit_key", 7, 1)

	// The string is one byte long: there is nothing but zeros past its end,
	// however far.
	for _, offset := range []int{8, 9, 15, 16, 100, 1000000, 4294967295} {
		expectBit(t, conn, "bit_key", offset, 0)
	}
	// Reading those bits doesn't make the string any longer.
	expectBitmap(t, srv, "bit_key", 0b00000001)
}

func TestGetBitOfAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bit_key", 2, 1, 0)
	for _, key := range []string{"new_key", "BIT_KEY", "bit_key ", ""} {
		for _, offset := range []int{0, 2, 7, 8, 4294967295} {
			expectBit(t, conn, key, offset, 0)
		}
	}

	// Reading a bit creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("bit_key"))
	do(t, conn, "TYPE", "new_key")
	expectReply(t, conn, "+none\r\n")
}

func TestGetBitOfARandomBitmap(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The bits can't be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	key := randomWord(t)
	bits := make([]int, 64)
	for range 100 {
		offset, bit := rng.Intn(len(bits)), rng.Intn(2)
		setBitTo(t, conn, key, offset, bit, bits[offset])
		bits[offset] = bit
	}
	for offset, want := range bits {
		expectBit(t, conn, key, offset, want)
	}
}

func TestGetBitInvalidOffset(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bit_key", 2, 1, 0)
	// The offset is checked whether the key exists or not.
	for _, key := range []string{"bit_key", "new_key"} {
		for _, offset := range []string{"-1", "abc", "", "1.5", "01", "+1", " 1", "1 ", "0x1", "4294967296", "9223372036854775808", "-0"} {
			do(t, conn, "GETBIT", key, offset)
			expectReply(t, conn, "-ERR bit offset is not an integer or out of range\r\n")
		}
	}
}

func TestGetBitWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"GETBIT"}, {"GETBIT", "bit_key"}, {"GETBIT", "bit_key", "2", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'getbit' command\r\n")
	}
}

func TestGetBitOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))
	zadd(t, conn, "zset_key", "1", "a")

	for _, key := range []string{"list_key", "stream_key", "zset_key"} {
		do(t, conn, "GETBIT", key, "2")
		expectReply(t, conn, wrongTypeError)
		// The offset is checked first.
		do(t, conn, "GETBIT", key, "-1")
		expectReply(t, conn, "-ERR bit offset is not an integer or out of range\r\n")
	}
}

func TestGetBitCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bit_key", 2, 1, 0)
	for _, name := range []string{"GETBIT", "getbit", "GetBit"} {
		do(t, conn, name, "bit_key", "2")
		expectReply(t, conn, ":1\r\n")
	}
}

func TestGetBitInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GETBIT", "bit_key", "2"},
		{"SETBIT", "bit_key", "2", "1"},
		{"GETBIT", "bit_key", "2"},
		{"GETBIT", "bit_key", "x"},
		{"SETBIT", "bit_key", "2", "0"},
		{"GETBIT", "bit_key", "2"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Each command sees the bitmap as the previous ones left it.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n", ":0\r\n", ":1\r\n", "-ERR bit offset is not an integer or out of range\r\n", ":1\r\n", ":0\r\n"))
}

func TestGetBitIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	setBitTo(t, conn, "bit_key", 2, 1, 0)
	expectPropagated(t, replica, "SETBIT", "bit_key", "2", "1")
	do(t, watcher, "WATCH", "bit_key", "new_key")
	expectReply(t, watcher, "+OK\r\n")

	expectBit(t, conn, "bit_key", 2, 1)
	expectBit(t, conn, "bit_key", 1000, 0)
	expectBit(t, conn, "new_key", 2, 0)

	// Reading a bit is neither logged nor propagated, and doesn't count as a
	// modification of the key.
	if got, want := readFile(t, incrPath), encodeCommand("SETBIT", "bit_key", "2", "1"); got != want {
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

func TestGetBitOfAKeyThatExpired(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	// "\xff" is 11111111.
	do(t, conn, "SET", "bit_key", "\xff", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	expectBit(t, conn, "bit_key", 2, 1)

	clock.Advance(100 * time.Millisecond)
	expectBit(t, conn, "bit_key", 2, 0)
}
