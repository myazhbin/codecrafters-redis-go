package main

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBitOpOr(t *testing.T) {
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
	do(t, conn, "BITOP", "OR", "dest", "key1", "key2")
	expectReply(t, conn, ":1\r\n")

	// Bits 0, 4 and 6 are set in at least one source.
	do(t, conn, "GETBIT", "dest", "4")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest", "0")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest", "6")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest", "1")
	expectReply(t, conn, ":0\r\n")
	expectBitmap(t, srv, "dest", 0b10001010)
}

func TestBitOpOrOfBitmapsOfDifferentLengths(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// key3 is 01000000 00100000 (2 bytes) and key4 is 01000000 (1 byte).
	do(t, conn, "SETBIT", "key3", "1", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SETBIT", "key3", "10", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SETBIT", "key4", "1", "1")
	expectReply(t, conn, ":0\r\n")

	// The destination is as long as the longest source.
	do(t, conn, "BITOP", "OR", "dest2", "key3", "key4")
	expectReply(t, conn, ":2\r\n")

	// The shorter source counts as 01000000 00000000: bit 10, which is past
	// its end, is set in the other one.
	do(t, conn, "GETBIT", "dest2", "10")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest2", "1")
	expectReply(t, conn, ":1\r\n")
	expectBitmap(t, srv, "dest2", 0b01000000, 0b00100000)

	// Whichever of the two comes first.
	expectBitOp(t, conn, 2, "OR", "dest3", "key4", "key3")
	expectBitmap(t, srv, "dest3", 0b01000000, 0b00100000)
}

func TestBitOpOrSetsTheBitsThatAreSetInAtLeastOneSource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Every combination of two bits: 1 or 1, 1 or 0, 0 or 1, 0 or 0.
	setString(t, conn, "key1", "\xcc") // 11001100
	setString(t, conn, "key2", "\xaa") // 10101010
	expectBitOp(t, conn, 1, "OR", "dest", "key1", "key2")
	expectBitmap(t, srv, "dest", 0b11101110)
	expectBitOp(t, conn, 1, "OR", "dest", "key2", "key1")
	expectBitmap(t, srv, "dest", 0b11101110)

	// Each byte with the byte that has its position.
	setString(t, conn, "key1", "\xf0\x0f\x00\x81")
	setString(t, conn, "key2", "\x0f\x0f\x00\x18")
	expectBitOp(t, conn, 4, "OR", "dest", "key1", "key2")
	expectBitmap(t, srv, "dest", 0xff, 0x0f, 0x00, 0x99)

	// "a" is 01100001 and "B" is 01000010: together they are "c".
	setString(t, conn, "word1", "aé")
	setString(t, conn, "word2", "B\x00\x00")
	expectBitOp(t, conn, 3, "OR", "word", "word1", "word2")
	do(t, conn, "GET", "word")
	expectReply(t, conn, bulk("cé"))
}

func TestBitOpOrWithAMissingSource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc\x01")
	setString(t, conn, "empty", "")

	// A key that doesn't exist is a string of zeros: it adds nothing.
	expectBitOp(t, conn, 2, "OR", "dest", "key1", "missing")
	expectBitmap(t, srv, "dest", 0xcc, 0x01)
	expectBitOp(t, conn, 2, "OR", "dest", "missing", "key1", "empty")
	expectBitmap(t, srv, "dest", 0xcc, 0x01)

	// With nothing but zeros to combine, and no length either, there is no
	// result: the destination is deleted.
	expectBitOp(t, conn, 0, "OR", "dest", "missing", "empty")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpOrOfSeveralSources(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "one", "\x01")
	setString(t, conn, "three", "\x02\x20\x40")
	setString(t, conn, "five", "\x04\x00\x80\x08\x10")

	for _, order := range [][]string{
		{"one", "three", "five"}, {"five", "three", "one"}, {"three", "one", "five"}, {"three", "five", "one"},
	} {
		expectBitOp(t, conn, 5, append([]string{"OR", "dest"}, order...)...)
		expectBitmap(t, srv, "dest", 0x07, 0x20, 0xc0, 0x08, 0x10)
	}
	// With a single source, the result is a copy of it.
	expectBitOp(t, conn, 3, "OR", "dest", "three")
	expectBitmap(t, srv, "dest", 0x02, 0x20, 0x40)
	// A source that is given twice counts once.
	expectBitOp(t, conn, 3, "OR", "dest", "three", "one", "three")
	expectBitmap(t, srv, "dest", 0x03, 0x20, 0x40)
}

func TestBitOpOrOfRandomStrings(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Neither the lengths nor the result can be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for range 30 {
		sources := make([][]byte, 2+rng.Intn(3))
		arguments := []string{"OR", "dest"}
		length := 0
		for i := range sources {
			sources[i] = make([]byte, rng.Intn(40))
			rng.Read(sources[i])
			// Mostly zeros, for some of them to be left in the result.
			for j := range sources[i] {
				sources[i][j] &= byte(rng.Intn(256))
			}
			key := "key" + strconv.Itoa(i)
			setString(t, conn, key, string(sources[i]))
			arguments = append(arguments, key)
			length = max(length, len(sources[i]))
		}

		want := make([]byte, length)
		for _, source := range sources {
			for i, b := range source {
				want[i] |= b
			}
		}

		expectBitOp(t, conn, length, arguments...)
		if length == 0 {
			do(t, conn, "TYPE", "dest")
			expectReply(t, conn, "+none\r\n")
			continue
		}
		expectBitmap(t, srv, "dest", want...)
	}
}

func TestBitOpOrWithAMuchLongerSource(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const length = 100000
	setString(t, conn, "long", strings.Repeat("\x01", length))
	setString(t, conn, "short", "\xf0\xf0")

	expectBitOp(t, conn, length, "OR", "dest", "short", "long")
	// The first two bytes are 11110001, the others 00000001.
	expectBitCount(t, conn, "dest", 10+length-2)
	expectBitCount(t, conn, "dest", 10, "0", "1")
	expectBitCount(t, conn, "dest", length-2, "2", "-1")
}

func TestBitOpOrReplacesTheDestination(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")

	// The destination can be a source, and what it held is replaced.
	expectBitOp(t, conn, 1, "OR", "key1", "key1", "key2")
	expectBitmap(t, srv, "key1", 0xee)
	expectBitmap(t, srv, "key2", 0xaa)

	setString(t, conn, "dest", "something much longer than the result")
	expectBitOp(t, conn, 1, "OR", "dest", "key2")
	expectBitmap(t, srv, "dest", 0xaa)
}

func TestBitOpOrNameIsCaseInsensitive(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")
	for i, name := range []string{"OR", "or", "Or", "oR"} {
		dest := "dest" + strconv.Itoa(i)
		expectBitOp(t, conn, 1, name, dest, "key1", "key2")
		expectBitmap(t, srv, dest, 0xee)
	}
	do(t, conn, "BITOP", "ORR", "dest", "key1", "key2")
	expectReply(t, conn, "-ERR syntax error\r\n")
	do(t, conn, "BITOP", "|", "dest", "key1", "key2")
	expectReply(t, conn, "-ERR syntax error\r\n")
}

func TestBitOpOrOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "key1", "\xcc")
	zadd(t, conn, "zset_key", "1", "a")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")

	for _, key := range []string{"zset_key", "list_key"} {
		do(t, conn, "BITOP", "OR", "dest", "key1", key)
		expectReply(t, conn, wrongTypeError)
	}
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpOrIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	commands := [][]string{
		{"SETBIT", "key1", "4", "1"},
		{"SETBIT", "key2", "6", "1"},
		{"BITOP", "OR", "dest", "key1", "key2"},
	}
	want := ""
	for _, command := range commands {
		do(t, conn, command...)
		drainPendingReply(t, conn)
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)

	// A server that restarts computes the same result again.
	restarted := restartWithAOF(t, srv, args...)
	expectBitmap(t, restarted, "dest", 0b00001010)
}

func TestBitOpOrAndBitOpAndInATransaction(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa\xff")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"BITOP", "OR", "either", "key1", "key2"},
		{"BITOP", "AND", "both", "key1", "key2"},
		{"BITOP", "AND", "dest", "either", "both"},
		{"BITCOUNT", "either"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":2\r\n", ":2\r\n", ":2\r\n", ":14\r\n"))
	expectBitmap(t, srv, "either", 0xee, 0xff)
	expectBitmap(t, srv, "both", 0x88, 0x00)
	expectBitmap(t, srv, "dest", 0x88, 0x00)
}
