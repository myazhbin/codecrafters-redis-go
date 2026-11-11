package main

import (
	"strconv"
	"testing"
)

func TestGetOfABitmapCreatedWithSetBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Setting bit 1 produces the byte 01000000, which is the character "@".
	do(t, conn, "SETBIT", "mykey", "1", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, "$1\r\n@\r\n")
}

func TestGetOfEachBitOfAByte(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Offset 0 is the most significant bit of the byte.
	for offset, want := range []string{"\x80", "@", " ", "\x10", "\x08", "\x04", "\x02", "\x01"} {
		key := "mykey_" + strconv.Itoa(offset)
		setBitTo(t, conn, key, offset, 1, 0)
		do(t, conn, "GET", key)
		expectReply(t, conn, bulk(want))
	}
}

func TestGetOfACharacterBuiltBitByBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "A" is 01000001.
	setBitTo(t, conn, "mykey", 1, 1, 0)
	setBitTo(t, conn, "mykey", 7, 1, 0)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("A"))

	// With bit 2 it is 01100001, which is "a".
	setBitTo(t, conn, "mykey", 2, 1, 0)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("a"))

	// And without bit 7 it is 01100000, which is "`".
	setBitTo(t, conn, "mykey", 7, 0, 1)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("`"))
}

func TestGetOfABitmapWhoseBitsAreAllZero(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The key holds a byte, even though none of its bits is set.
	setBitTo(t, conn, "mykey", 3, 0, 0)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, "$1\r\n\x00\r\n")

	// Clearing the bits of a bitmap doesn't delete it either.
	setBitTo(t, conn, "other", 3, 1, 0)
	setBitTo(t, conn, "other", 3, 0, 1)
	do(t, conn, "GET", "other")
	expectReply(t, conn, "$1\r\n\x00\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("mykey", "other"))
}

func TestGetOfAStringChangedWithSetBit(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "A" is 01000001; with bit 6 set it is 01000011, which is "C".
	do(t, conn, "SET", "mykey", "A")
	expectReply(t, conn, "+OK\r\n")
	setBitTo(t, conn, "mykey", 6, 1, 0)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("C"))
	setBitTo(t, conn, "mykey", 6, 0, 1)
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("A"))

	// Only the byte that holds the bit changes: "hello" becomes "hallo" when
	// bit 13 is cleared ("e" is 01100101, "a" is 01100001).
	do(t, conn, "SET", "greeting", "hello")
	expectReply(t, conn, "+OK\r\n")
	setBitTo(t, conn, "greeting", 13, 0, 1)
	do(t, conn, "GET", "greeting")
	expectReply(t, conn, bulk("hallo"))
}

func TestStringCommandsWorkOnABitmap(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "1" is 00110001.
	for _, offset := range []int{2, 3, 7} {
		setBitTo(t, conn, "counter", offset, 1, 0)
	}
	do(t, conn, "TYPE", "counter")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "GET", "counter")
	expectReply(t, conn, bulk("1"))
	// A bitmap whose bytes are digits is a number like any other.
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":2\r\n")
	// "2" is 00110010.
	expectBits(t, conn, "counter", "2")
}

func TestGetOfABitmapAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	setBitTo(t, conn, "mykey", 1, 1, 0)
	setBitTo(t, conn, "mykey", 7, 1, 0)
	setBitTo(t, conn, "mykey", 1, 1, 1)

	// The bitmap is rebuilt from the log, bit by bit.
	conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, bulk("A"))
}
