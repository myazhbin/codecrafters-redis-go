package main

import (
	"crypto/rand"
	"net"
	"strconv"
	"testing"
	"time"
)

// setString stores a string with SET.
func setString(t *testing.T, conn net.Conn, key, value string) {
	t.Helper()

	do(t, conn, "SET", key, value)
	expectReply(t, conn, "+OK\r\n")
}

// expectBitOp sends BITOP with the given arguments and checks its reply: the
// length of the result, in bytes.
func expectBitOp(t *testing.T, conn net.Conn, length int, arguments ...string) {
	t.Helper()

	do(t, conn, append([]string{"BITOP"}, arguments...)...)
	expectReply(t, conn, ":"+strconv.Itoa(length)+"\r\n")
}

func TestBitOpAnd(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// key1 is 10001000 and key2 is 10000010.
	for _, offset := range []string{"0", "4"} {
		do(t, conn, "SETBIT", "key1", offset, "1")
		expectReply(t, conn, ":0\r\n")
	}
	for _, offset := range []string{"0", "6"} {
		do(t, conn, "SETBIT", "key2", offset, "1")
		expectReply(t, conn, ":0\r\n")
	}

	// The reply is the length of the destination string, in bytes.
	do(t, conn, "BITOP", "AND", "dest", "key1", "key2")
	expectReply(t, conn, ":1\r\n")

	// Only bit 0 is set in both.
	do(t, conn, "GETBIT", "dest", "0")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest", "4")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GETBIT", "dest", "6")
	expectReply(t, conn, ":0\r\n")
	expectBitmap(t, srv, "dest", 0b10000000)
}

func TestBitOpAndSetsTheBitsThatAreSetInEverySource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Every combination of two bits: 1 and 1, 1 and 0, 0 and 1, 0 and 0.
	setString(t, conn, "key1", "\xcc") // 11001100
	setString(t, conn, "key2", "\xaa") // 10101010
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	expectBitmap(t, srv, "dest", 0b10001000)

	// The order of the sources doesn't matter.
	expectBitOp(t, conn, 1, "AND", "other", "key2", "key1")
	expectBitmap(t, srv, "other", 0b10001000)
}

func TestBitOpAndOfStringsOfSeveralBytes(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xff\x0f\xaa\x00")
	setString(t, conn, "key2", "\x0f\xff\x55\xff")
	expectBitOp(t, conn, 4, "AND", "dest", "key1", "key2")
	expectBitmap(t, srv, "dest", 0x0f, 0x0f, 0x00, 0x00)
	do(t, conn, "STRLEN", "dest")
	expectReply(t, conn, ":4\r\n")

	// Strings made of text are bytes like any others: "a" AND "c" is "a",
	// and a lower-case letter AND a space is a space.
	setString(t, conn, "word1", "abc")
	setString(t, conn, "word2", "c  ")
	expectBitOp(t, conn, 3, "AND", "word", "word1", "word2")
	do(t, conn, "GET", "word")
	expectReply(t, conn, bulk("a  "))
}

func TestBitOpAndOfTextThatIsNotASCII(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The characters of a text can take several bytes each ("é" is the two
	// bytes c3 a9): every one of those bytes is combined, not just the first
	// of each character.
	setString(t, conn, "ones", "\xff\xff\xff\xff\xff\xff\xff")
	setString(t, conn, "text", "é✓é")
	expectBitOp(t, conn, 7, "AND", "dest", "ones", "text")
	expectBitmap(t, srv, "dest", 0xc3, 0xa9, 0xe2, 0x9c, 0x93, 0xc3, 0xa9)
	expectBitOp(t, conn, 7, "AND", "dest", "text", "ones")
	expectBitmap(t, srv, "dest", 0xc3, 0xa9, 0xe2, 0x9c, 0x93, 0xc3, 0xa9)

	setString(t, conn, "mask", "\xf0\x0f\xf0\x0f\xf0\x0f\xf0")
	expectBitOp(t, conn, 7, "AND", "dest", "mask", "text")
	expectBitmap(t, srv, "dest", 0xc0, 0x09, 0xe0, 0x0c, 0x90, 0x03, 0xa0)
}

func TestBitOpAndOfRandomStrings(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The result can't be hardcoded.
	for range 5 {
		first, second := make([]byte, 64), make([]byte, 64)
		if _, err := rand.Read(first); err != nil {
			t.Fatalf("generating random bytes: %v", err)
		}
		if _, err := rand.Read(second); err != nil {
			t.Fatalf("generating random bytes: %v", err)
		}
		want := make([]byte, 64)
		for i := range want {
			want[i] = first[i] & second[i]
		}

		setString(t, conn, "key1", string(first))
		setString(t, conn, "key2", string(second))
		expectBitOp(t, conn, 64, "AND", "dest", "key1", "key2")
		expectBitmap(t, srv, "dest", want...)
	}
}

func TestBitOpAndOfMoreOrFewerThanTwoSources(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xff\xf0")
	setString(t, conn, "key2", "\x3c\xff")
	setString(t, conn, "key3", "\x0f\x9f")

	expectBitOp(t, conn, 2, "AND", "dest", "key1", "key2", "key3")
	expectBitmap(t, srv, "dest", 0x0c, 0x90)
	// A source can be given several times.
	expectBitOp(t, conn, 2, "AND", "dest", "key1", "key2", "key1", "key2")
	expectBitmap(t, srv, "dest", 0x3c, 0xf0)
	// With a single source, the result is a copy of it.
	expectBitOp(t, conn, 2, "AND", "dest", "key2")
	expectBitmap(t, srv, "dest", 0x3c, 0xff)
}

func TestBitOpLeavesTheSourcesAlone(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	expectBitmap(t, srv, "key1", 0xcc)
	expectBitmap(t, srv, "key2", 0xaa)

	// And the result doesn't follow them afterwards.
	setBitTo(t, conn, "key1", 7, 1, 0)
	setBitTo(t, conn, "key2", 7, 1, 0)
	expectBitmap(t, srv, "dest", 0b10001000)
}

func TestBitOpReplacesTheDestination(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")

	// Whatever the destination held, including its expiry and a value of
	// another type, is gone.
	do(t, conn, "SET", "dest", "a long string that was there before", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	do(t, conn, "GET", "dest")
	expectReply(t, conn, bulk("\x88"))
	clock.Advance(time.Hour)
	do(t, conn, "GET", "dest")
	expectReply(t, conn, bulk("\x88"))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	expectBitOp(t, conn, 1, "AND", "list_key", "key1", "key2")
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "GET", "list_key")
	expectReply(t, conn, bulk("\x88"))
}

func TestBitOpDestinationCanBeASource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")
	expectBitOp(t, conn, 1, "AND", "key1", "key1", "key2")
	expectBitmap(t, srv, "key1", 0b10001000)
	expectBitmap(t, srv, "key2", 0xaa)
}

func TestBitOpAndOfSourcesThatDoNotExist(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// There is nothing to combine: the result is empty, and isn't stored.
	expectBitOp(t, conn, 0, "AND", "dest", "missing1", "missing2")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	// A destination that exists is deleted: it holds the result, which is
	// nothing. Empty strings are no different from missing keys.
	setString(t, conn, "dest", "something")
	setString(t, conn, "empty", "")
	expectBitOp(t, conn, 0, "AND", "dest", "missing1", "empty")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "key1", "\xcc")
	for _, command := range [][]string{{"BITOP"}, {"BITOP", "AND"}, {"BITOP", "AND", "dest"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'bitop' command\r\n")
	}
	for _, operation := range []string{"NAND", "", "&", "ANDD", "AN"} {
		do(t, conn, "BITOP", operation, "dest", "key1", "key1")
		expectReply(t, conn, "-ERR syntax error\r\n")
	}
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "key1", "\xcc")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))
	zadd(t, conn, "zset_key", "1", "a")

	// A source has to be a string, wherever it is among the sources.
	for _, key := range []string{"list_key", "stream_key", "zset_key"} {
		do(t, conn, "BITOP", "AND", "dest", key, "key1")
		expectReply(t, conn, wrongTypeError)
		do(t, conn, "BITOP", "AND", "dest", "key1", "missing", key)
		expectReply(t, conn, wrongTypeError)
	}
	// Nothing was stored.
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpNamesAreCaseInsensitive(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")
	for i, names := range [][2]string{{"BITOP", "AND"}, {"bitop", "and"}, {"BitOp", "And"}} {
		dest := "dest" + strconv.Itoa(i)
		do(t, conn, names[0], names[1], dest, "key1", "key2")
		expectReply(t, conn, ":1\r\n")
		expectBitmap(t, srv, dest, 0b10001000)
	}
	// The keys are another matter.
	expectBitOp(t, conn, 0, "AND", "dest", "KEY1", "Key2")
}

func TestBitOpIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	commands := [][]string{
		{"SET", "key1", "\xcc"},
		{"SET", "key2", "\xaa"},
		{"BITOP", "AND", "dest", "key1", "key2"},
		// Storing the same result again is a write all the same.
		{"BITOP", "AND", "dest", "key1", "key2"},
		// So is deleting a destination because the result is empty.
		{"BITOP", "AND", "dest", "missing"},
	}
	want := ""
	for _, command := range commands {
		do(t, conn, command...)
		drainPendingReply(t, conn)
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}

	// With nothing to store and nothing to delete, nothing is written. A
	// command that fails writes nothing either.
	expectBitOp(t, conn, 0, "AND", "dest", "missing")
	do(t, conn, "BITOP", "NAND", "dest", "key1")
	expectReply(t, conn, "-ERR syntax error\r\n")

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestBitOpIsReplayedAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "key1", 0, 1, 0)
	setBitTo(t, conn, "key1", 4, 1, 0)
	setBitTo(t, conn, "key2", 0, 1, 0)
	setBitTo(t, conn, "key2", 6, 1, 0)
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	// The sources change after the result was computed.
	setBitTo(t, conn, "key2", 4, 1, 0)

	// The log is replayed in order: the result is the one of the time.
	restarted := restartWithAOF(t, srv, args...)
	expectBitmap(t, restarted, "dest", 0b10000000)
	expectBitmap(t, restarted, "key2", 0b10001010)
}

func TestBitOpIsAppliedByReplicas(t *testing.T) {
	master := startFakeMaster(t)
	srv, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"SET", "key1", "\xcc"}, []string{"SET", "key2", "\xaa"}, []string{"BITOP", "AND", "dest", "key1", "key2"})

	conn := dial(t, addr)
	expectEventually(t, conn, ":1\r\n", "STRLEN", "dest")
	expectBitmap(t, srv, "dest", 0b10001000)
}

func TestBitOpInATransaction(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	conn, other := dial(t, addr), dial(t, addr)

	setString(t, conn, "key1", "\xcc")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"SET", "key2", "\xaa"},
		{"BITOP", "AND", "dest", "key1", "key2"},
		{"BITOP", "NAND", "dest", "key1", "key2"},
		{"GETBIT", "dest", "0"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Nothing is stored before the transaction is executed.
	do(t, other, "TYPE", "dest")
	expectReply(t, other, "+none\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":1\r\n", "-ERR syntax error\r\n", ":1\r\n"))
	expectBitmap(t, srv, "dest", 0b10001000)
}

func TestBitOpModifiesTheDestinationOnly(t *testing.T) {
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

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")

	// The sources are only read.
	do(t, watcher, "WATCH", "key1", "key2")
	expectReply(t, watcher, "+OK\r\n")
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	transaction(array("+PONG\r\n"))

	// The destination is written, and deleted when the result is empty.
	do(t, watcher, "WATCH", "dest")
	expectReply(t, watcher, "+OK\r\n")
	expectBitOp(t, conn, 1, "AND", "dest", "key1", "key2")
	transaction("*-1\r\n")
	do(t, watcher, "WATCH", "dest")
	expectReply(t, watcher, "+OK\r\n")
	expectBitOp(t, conn, 0, "AND", "dest", "missing")
	transaction("*-1\r\n")
}
