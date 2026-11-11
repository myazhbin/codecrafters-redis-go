package main

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// storedString returns the string stored at key, read straight from the
// server's keyspace, and whether the key exists.
func storedString(t *testing.T, srv *Server, key string) (string, bool) {
	t.Helper()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	value, ok, err := srv.store.getString(key)
	if err != nil {
		t.Fatalf("reading string %q: %v", key, err)
	}
	return value, ok
}

// expectBitmap checks that the string stored at key on srv is made of exactly
// the given bytes.
func expectBitmap(t *testing.T, srv *Server, key string, want ...byte) {
	t.Helper()

	got, ok := storedString(t, srv, key)
	if !ok {
		t.Errorf("key %q doesn't exist, want it to hold the bytes %08b", key, want)
		return
	}
	if got != string(want) {
		t.Errorf("key %q holds the bytes %08b, want %08b", key, []byte(got), want)
	}
}

// setBitTo sends SETBIT key offset bit and checks the reply, which is the
// value the bit had before.
func setBitTo(t *testing.T, conn net.Conn, key string, offset, bit, old int) {
	t.Helper()

	do(t, conn, "SETBIT", key, strconv.Itoa(offset), strconv.Itoa(bit))
	expectReply(t, conn, ":"+strconv.Itoa(old)+"\r\n")
}

func TestSetBitCreatesABitmap(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The reply is the original bit at that offset, as a RESP integer: on a
	// new key, that is always 0.
	do(t, conn, "SETBIT", "bitmap_key", "3", "1")
	expectReply(t, conn, ":0\r\n")

	// Offset 0 is the most significant bit of the first byte.
	expectBitmap(t, srv, "bitmap_key", 0b00010000)
}

func TestSetBitNumbersBitsFromTheLeft(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	for offset, want := range []byte{0b10000000, 0b01000000, 0b00100000, 0b00010000, 0b00001000, 0b00000100, 0b00000010, 0b00000001} {
		key := "bitmap_" + strconv.Itoa(offset)
		setBitTo(t, conn, key, offset, 1, 0)
		expectBitmap(t, srv, key, want)
	}
}

func TestSetBitRepliesWithTheOriginalBit(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "bitmap_key", 3, 1, 0)
	// The bit is 1 now, whatever it is set to next.
	setBitTo(t, conn, "bitmap_key", 3, 1, 1)
	setBitTo(t, conn, "bitmap_key", 3, 0, 1)
	setBitTo(t, conn, "bitmap_key", 3, 0, 0)
	setBitTo(t, conn, "bitmap_key", 3, 1, 0)
	expectBitmap(t, srv, "bitmap_key", 0b00010000)
}

func TestSetBitLeavesTheOtherBitsAlone(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// newsletter_subscribers: 10001010
	for _, offset := range []int{0, 4, 6} {
		setBitTo(t, conn, "newsletter_subscribers", offset, 1, 0)
	}
	expectBitmap(t, srv, "newsletter_subscribers", 0b10001010)

	// The bits that weren't set are 0.
	for _, offset := range []int{1, 2, 3, 5, 7} {
		setBitTo(t, conn, "newsletter_subscribers", offset, 0, 0)
	}
	expectBitmap(t, srv, "newsletter_subscribers", 0b10001010)

	setBitTo(t, conn, "newsletter_subscribers", 4, 0, 1)
	expectBitmap(t, srv, "newsletter_subscribers", 0b10000010)
	setBitTo(t, conn, "newsletter_subscribers", 7, 1, 0)
	expectBitmap(t, srv, "newsletter_subscribers", 0b10000011)
}

func TestSetBitToZeroCreatesTheKeyToo(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "bitmap_key", 5, 0, 0)
	expectBitmap(t, srv, "bitmap_key", 0)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("bitmap_key"))
}

func TestBitmapIsAString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setBitTo(t, conn, "bitmap_key", 1, 1, 0)
	do(t, conn, "TYPE", "bitmap_key")
	expectReply(t, conn, "+string\r\n")
	// 01000000 is the character "@".
	do(t, conn, "GET", "bitmap_key")
	expectReply(t, conn, bulk("@"))
}

func TestSetBitWithRandomKeysAndOffsets(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Neither the key nor the bit can be hardcoded.
	for offset := range 8 {
		key := randomWord(t)
		setBitTo(t, conn, key, offset, 1, 0)
		expectBitmap(t, srv, key, 0x80>>offset)
		setBitTo(t, conn, key, offset, 1, 1)
	}
}

func TestSetBitInvalidOffset(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, offset := range []string{"-1", "abc", "", "1.5", "01", "+1", " 1", "1 ", "0x1", "4294967296", "9223372036854775808", "-0"} {
		do(t, conn, "SETBIT", "bitmap_key", offset, "1")
		expectReply(t, conn, "-ERR bit offset is not an integer or out of range\r\n")
	}
	// The offset is looked at before the bit is.
	do(t, conn, "SETBIT", "bitmap_key", "-1", "2")
	expectReply(t, conn, "-ERR bit offset is not an integer or out of range\r\n")

	// A command that fails creates nothing.
	do(t, conn, "TYPE", "bitmap_key")
	expectReply(t, conn, "+none\r\n")
}

func TestSetBitInvalidValue(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, bit := range []string{"2", "-1", "a", "", "01", "+1", " 1", "1.0", "true", "10"} {
		do(t, conn, "SETBIT", "bitmap_key", "3", bit)
		expectReply(t, conn, "-ERR bit is not an integer or out of range\r\n")
	}
	do(t, conn, "TYPE", "bitmap_key")
	expectReply(t, conn, "+none\r\n")
}

func TestSetBitWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"SETBIT"}, {"SETBIT", "bitmap_key"}, {"SETBIT", "bitmap_key", "3"}, {"SETBIT", "bitmap_key", "3", "1", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'setbit' command\r\n")
	}
}

func TestSetBitOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))
	zadd(t, conn, "zset_key", "1", "a")

	for _, key := range []string{"list_key", "stream_key", "zset_key"} {
		do(t, conn, "SETBIT", key, "3", "1")
		expectReply(t, conn, wrongTypeError)
		// The arguments are checked first.
		do(t, conn, "SETBIT", key, "-1", "1")
		expectReply(t, conn, "-ERR bit offset is not an integer or out of range\r\n")
		do(t, conn, "SETBIT", key, "3", "2")
		expectReply(t, conn, "-ERR bit is not an integer or out of range\r\n")
	}
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+list\r\n")
}

func TestSetBitCommandNameIsCaseInsensitive(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	for offset, name := range []string{"SETBIT", "setbit", "SetBit"} {
		do(t, conn, name, "bitmap_key", strconv.Itoa(offset), "1")
		expectReply(t, conn, ":0\r\n")
	}
	expectBitmap(t, srv, "bitmap_key", 0b11100000)
}

func TestBitmapsAreIndependent(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "first", 0, 1, 0)
	// The same bit of another key is another bit.
	setBitTo(t, conn, "second", 0, 1, 0)
	setBitTo(t, conn, "second", 7, 1, 0)
	setBitTo(t, conn, "first", 0, 0, 1)

	expectBitmap(t, srv, "first", 0)
	expectBitmap(t, srv, "second", 0b10000001)
}

func TestSetBitKeepsTheExpiryOfTheKey(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "key", "@", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	// "@" is 01000000.
	setBitTo(t, conn, "key", 1, 0, 1)
	setBitTo(t, conn, "key", 7, 1, 0)

	// The key still expires when it was going to.
	clock.Advance(99 * time.Millisecond)
	do(t, conn, "GET", "key")
	expectReply(t, conn, bulk("\x01"))
	clock.Advance(time.Millisecond)
	do(t, conn, "GET", "key")
	expectReply(t, conn, "$-1\r\n")

	// A bitmap that SETBIT creates never expires.
	setBitTo(t, conn, "key", 7, 1, 0)
	clock.Advance(time.Hour)
	do(t, conn, "GET", "key")
	expectReply(t, conn, bulk("\x01"))
}

func TestSetBitIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	// Creating the key is a write, even with a bit that is 0.
	commands := [][]string{
		{"SETBIT", "bitmap_key", "3", "1"},
		{"SETBIT", "bitmap_key", "6", "1"},
		{"SETBIT", "bitmap_key", "3", "0"},
		{"SETBIT", "other_key", "0", "0"},
	}
	want := ""
	for _, command := range commands {
		do(t, conn, command...)
		drainPendingReply(t, conn)
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}

	// Setting a bit to the value it has changes nothing: it isn't logged or
	// propagated. Neither is a command that fails.
	setBitTo(t, conn, "bitmap_key", 6, 1, 1)
	setBitTo(t, conn, "bitmap_key", 0, 0, 0)
	do(t, conn, "SETBIT", "bitmap_key", "0", "2")
	expectReply(t, conn, "-ERR bit is not an integer or out of range\r\n")

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)

	// A server that restarts rebuilds the same bitmaps.
	restarted := restartWithAOF(t, srv, args...)
	expectBitmap(t, restarted, "bitmap_key", 0b00000010)
	expectBitmap(t, restarted, "other_key", 0)
}

func TestSetBitIsAppliedByReplicas(t *testing.T) {
	master := startFakeMaster(t)
	srv, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"SETBIT", "bitmap_key", "1", "1"}, []string{"SETBIT", "bitmap_key", "7", "1"})

	conn := dial(t, addr)
	expectEventually(t, conn, bulk("A"), "GET", "bitmap_key")
	expectBitmap(t, srv, "bitmap_key", 0b01000001)
}

func TestSetBitInATransaction(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"SETBIT", "bitmap_key", "3", "1"},
		{"SETBIT", "bitmap_key", "3", "1"},
		{"SETBIT", "bitmap_key", "9", "9"},
		{"SETBIT", "bitmap_key", "3", "0"},
		{"SETBIT", "bitmap_key", "0", "1"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Nothing is created before the transaction is executed.
	do(t, other, "TYPE", "bitmap_key")
	expectReply(t, other, "+none\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n", ":1\r\n", "-ERR bit is not an integer or out of range\r\n", ":1\r\n", ":0\r\n"))
	expectBitmap(t, srv, "bitmap_key", 0b10000000)
}

func TestSetBitModifiesAWatchedKeyOnlyWhenItChangesSomething(t *testing.T) {
	addr := startTestServer(t)
	conn, watcher := dial(t, addr), dial(t, addr)
	transaction := func(want string) {
		t.Helper()
		do(t, watcher, "MULTI")
		expectReply(t, watcher, "+OK\r\n")
		do(t, watcher, "PING")
		expectReply(t, watcher, "+QUEUED\r\n")
		do(t, watcher, "EXEC")
		expectReply(t, watcher, want)
	}

	// Creating the key and changing a bit are modifications.
	for _, bit := range []int{1, 0} {
		do(t, watcher, "WATCH", "bitmap_key")
		expectReply(t, watcher, "+OK\r\n")
		setBitTo(t, conn, "bitmap_key", 3, bit, 1-bit)
		transaction("*-1\r\n")
	}

	// Setting a bit to the value it already has isn't.
	do(t, watcher, "WATCH", "bitmap_key")
	expectReply(t, watcher, "+OK\r\n")
	setBitTo(t, conn, "bitmap_key", 3, 0, 0)
	setBitTo(t, conn, "bitmap_key", 5, 0, 0)
	transaction(array("+PONG\r\n"))
}
