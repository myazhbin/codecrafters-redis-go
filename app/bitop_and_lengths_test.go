package main

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBitOpAndOfBitmapsOfDifferentLengths(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// key1 is 01000000 00100000 (2 bytes) and key2 is 01000000 (1 byte).
	do(t, conn, "SETBIT", "key1", "1", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SETBIT", "key1", "10", "1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SETBIT", "key2", "1", "1")
	expectReply(t, conn, ":0\r\n")

	// The destination is as long as the longest source.
	do(t, conn, "BITOP", "AND", "dest", "key1", "key2")
	expectReply(t, conn, ":2\r\n")

	// The shorter source counts as 01000000 00000000: only bit 1 is set in
	// both.
	do(t, conn, "GETBIT", "dest", "1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "dest", "10")
	expectReply(t, conn, ":0\r\n")
	expectBitmap(t, srv, "dest", 0b01000000, 0)
	do(t, conn, "STRLEN", "dest")
	expectReply(t, conn, ":2\r\n")
}

func TestBitOpAndWithTheShorterSourceFirst(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "long", "\xff\xff\xff")
	setString(t, conn, "short", "\x3c")

	// Whichever comes first, the result is as long as the longest.
	expectBitOp(t, conn, 3, "AND", "dest", "short", "long")
	expectBitmap(t, srv, "dest", 0x3c, 0, 0)
	expectBitOp(t, conn, 3, "AND", "dest", "long", "short")
	expectBitmap(t, srv, "dest", 0x3c, 0, 0)
}

func TestBitOpAndWithAMissingSource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "key1", "\xff\xff")

	// A key that doesn't exist is a string of zeros, as long as it takes:
	// nothing is set in both.
	expectBitOp(t, conn, 2, "AND", "dest", "key1", "missing")
	expectBitmap(t, srv, "dest", 0, 0)
	expectBitOp(t, conn, 2, "AND", "dest", "missing", "key1")
	expectBitmap(t, srv, "dest", 0, 0)
	// The destination exists, although all of its bits are 0.
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+string\r\n")
	expectBitCount(t, conn, "dest", 0)

	// An empty string is no different.
	setString(t, conn, "empty", "")
	expectBitOp(t, conn, 2, "AND", "dest", "empty", "key1")
	expectBitmap(t, srv, "dest", 0, 0)

	// The missing key wasn't created.
	do(t, conn, "TYPE", "missing")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpAndOfSeveralSourcesOfDifferentLengths(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "one", "\xf0")
	setString(t, conn, "three", "\xff\xff\xff")
	setString(t, conn, "five", "\xaa\xaa\xaa\xaa\xaa")

	// Bits can only be set where every source has a byte.
	for _, order := range [][]string{
		{"one", "three", "five"}, {"five", "three", "one"}, {"three", "one", "five"}, {"three", "five", "one"},
	} {
		expectBitOp(t, conn, 5, append([]string{"AND", "dest"}, order...)...)
		expectBitmap(t, srv, "dest", 0xa0, 0, 0, 0, 0)
	}
	expectBitOp(t, conn, 5, "AND", "dest", "three", "five")
	expectBitmap(t, srv, "dest", 0xaa, 0xaa, 0xaa, 0, 0)
	expectBitOp(t, conn, 5, "AND", "dest", "five", "three")
	expectBitmap(t, srv, "dest", 0xaa, 0xaa, 0xaa, 0, 0)
	expectBitOp(t, conn, 3, "AND", "dest", "one", "three")
	expectBitmap(t, srv, "dest", 0xf0, 0, 0)
}

func TestBitOpAndOfRandomStringsOfDifferentLengths(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Neither the lengths nor the result can be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for range 30 {
		sources := make([][]byte, 2+rng.Intn(3))
		arguments := []string{"AND", "dest"}
		length := 0
		for i := range sources {
			sources[i] = make([]byte, rng.Intn(40))
			rng.Read(sources[i])
			// Mostly ones, for some of them to be left in the result.
			for j := range sources[i] {
				sources[i][j] |= byte(rng.Intn(256))
			}
			key := "key" + strconv.Itoa(i)
			setString(t, conn, key, string(sources[i]))
			arguments = append(arguments, key)
			length = max(length, len(sources[i]))
		}

		want := make([]byte, length)
		for i := range want {
			want[i] = 0xff
			for _, source := range sources {
				if i < len(source) {
					want[i] &= source[i]
				} else {
					want[i] = 0
				}
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

func TestBitOpAndWithAMuchLongerSource(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const length = 100000
	setString(t, conn, "long", strings.Repeat("\xff", length))
	setString(t, conn, "short", "\xff\xff")

	expectBitOp(t, conn, length, "AND", "dest", "long", "short")
	do(t, conn, "STRLEN", "dest")
	expectReply(t, conn, ":"+strconv.Itoa(length)+"\r\n")
	// What is set is what the two have in common: the first two bytes.
	expectBitCount(t, conn, "dest", 16)
	expectBitCount(t, conn, "dest", 16, "0", "1")
	expectBitCount(t, conn, "dest", 0, "2", "-1")
}

func TestBitOpAndDestinationCanBeTheShorterSource(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "long", "\xff\xff\xff")
	setString(t, conn, "short", "\x3c")

	// The key grows to the length of the result.
	expectBitOp(t, conn, 3, "AND", "short", "long", "short")
	expectBitmap(t, srv, "short", 0x3c, 0, 0)
	expectBitmap(t, srv, "long", 0xff, 0xff, 0xff)
}
