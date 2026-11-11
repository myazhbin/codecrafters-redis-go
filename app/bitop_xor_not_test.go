package main

import (
	"math/rand"
	"strconv"
	"testing"
	"time"
)

// The course stops at AND and OR. BITOP has two more operations of the same
// kind, which are tested here.

func TestBitOpXor(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// A bit is set when it is set in one source and not in the other.
	setString(t, conn, "key1", "\xcc") // 11001100
	setString(t, conn, "key2", "\xaa") // 10101010
	expectBitOp(t, conn, 1, "XOR", "dest", "key1", "key2")
	expectBitmap(t, srv, "dest", 0b01100110)
	expectBitOp(t, conn, 1, "xor", "dest", "key2", "key1")
	expectBitmap(t, srv, "dest", 0b01100110)

	// With more sources, when it is set in an odd number of them.
	setString(t, conn, "key3", "\xf0") // 11110000
	expectBitOp(t, conn, 1, "XOR", "dest", "key1", "key2", "key3")
	expectBitmap(t, srv, "dest", 0b10010110)
	// A string XOR itself is all zeros, of the same length.
	expectBitOp(t, conn, 1, "XOR", "dest", "key1", "key1")
	expectBitmap(t, srv, "dest", 0)
}

func TestBitOpXorOfSourcesOfDifferentLengths(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	setString(t, conn, "long", "\xff\x0f\xaa")
	setString(t, conn, "short", "\x0f")

	// The bytes the shorter source doesn't have are zeros: they change
	// nothing.
	expectBitOp(t, conn, 3, "XOR", "dest", "long", "short")
	expectBitmap(t, srv, "dest", 0xf0, 0x0f, 0xaa)
	expectBitOp(t, conn, 3, "XOR", "dest", "short", "long")
	expectBitmap(t, srv, "dest", 0xf0, 0x0f, 0xaa)
	expectBitOp(t, conn, 3, "XOR", "dest", "missing", "long")
	expectBitmap(t, srv, "dest", 0xff, 0x0f, 0xaa)
	expectBitOp(t, conn, 0, "XOR", "dest", "missing", "other")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpXorOfRandomStrings(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for range 30 {
		sources := make([][]byte, 2+rng.Intn(3))
		arguments := []string{"XOR", "dest"}
		length := 0
		for i := range sources {
			sources[i] = make([]byte, 1+rng.Intn(40))
			rng.Read(sources[i])
			key := "key" + strconv.Itoa(i)
			setString(t, conn, key, string(sources[i]))
			arguments = append(arguments, key)
			length = max(length, len(sources[i]))
		}

		want := make([]byte, length)
		for _, source := range sources {
			for i, b := range source {
				want[i] ^= b
			}
		}
		expectBitOp(t, conn, length, arguments...)
		expectBitmap(t, srv, "dest", want...)
	}
}

func TestBitOpNot(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Every bit is inverted.
	setString(t, conn, "key1", "\xcc\x00\xff\x0f")
	expectBitOp(t, conn, 4, "NOT", "dest", "key1")
	expectBitmap(t, srv, "dest", 0x33, 0xff, 0x00, 0xf0)
	expectBitmap(t, srv, "key1", 0xcc, 0x00, 0xff, 0x0f)

	// Twice, and the string is back.
	expectBitOp(t, conn, 4, "not", "dest", "dest")
	expectBitmap(t, srv, "dest", 0xcc, 0x00, 0xff, 0x0f)

	// "é" is the bytes c3 a9.
	setString(t, conn, "text", "é")
	expectBitOp(t, conn, 2, "NOT", "dest", "text")
	expectBitmap(t, srv, "dest", 0x3c, 0x56)
}

func TestBitOpNotOfNothing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A string that has no bit has no bit to invert: the result is empty,
	// and the destination deleted.
	setString(t, conn, "dest", "something")
	expectBitOp(t, conn, 0, "NOT", "dest", "missing")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")
}

func TestBitOpNotTakesASingleSource(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "key1", "\xcc")
	setString(t, conn, "key2", "\xaa")
	do(t, conn, "BITOP", "NOT", "dest", "key1", "key2")
	expectReply(t, conn, "-ERR BITOP NOT must be called with a single source key.\r\n")
	do(t, conn, "BITOP", "NOT", "dest", "key1", "key2", "key1")
	expectReply(t, conn, "-ERR BITOP NOT must be called with a single source key.\r\n")
	do(t, conn, "BITOP", "NOT", "dest")
	expectReply(t, conn, "-ERR wrong number of arguments for 'bitop' command\r\n")
	do(t, conn, "TYPE", "dest")
	expectReply(t, conn, "+none\r\n")

	// The source has to be a string.
	zadd(t, conn, "zset_key", "1", "a")
	do(t, conn, "BITOP", "NOT", "dest", "zset_key")
	expectReply(t, conn, wrongTypeError)
}

func TestBitOpOperationsAgree(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// For strings of the same length, a OR b is NOT (NOT a AND NOT b), and
	// a XOR b is (a OR b) AND NOT (a AND b).
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	first, second := make([]byte, 32), make([]byte, 32)
	rng.Read(first)
	rng.Read(second)
	setString(t, conn, "a", string(first))
	setString(t, conn, "b", string(second))

	expectBitOp(t, conn, 32, "OR", "or", "a", "b")
	expectBitOp(t, conn, 32, "NOT", "not_a", "a")
	expectBitOp(t, conn, 32, "NOT", "not_b", "b")
	expectBitOp(t, conn, 32, "AND", "neither", "not_a", "not_b")
	expectBitOp(t, conn, 32, "NOT", "or_again", "neither")
	or, _ := storedString(t, srv, "or")
	orAgain, _ := storedString(t, srv, "or_again")
	if or != orAgain {
		t.Errorf("a OR b = %08b, but NOT (NOT a AND NOT b) = %08b", []byte(or), []byte(orAgain))
	}

	expectBitOp(t, conn, 32, "XOR", "xor", "a", "b")
	expectBitOp(t, conn, 32, "AND", "and", "a", "b")
	expectBitOp(t, conn, 32, "NOT", "not_and", "and")
	expectBitOp(t, conn, 32, "AND", "xor_again", "or", "not_and")
	xor, _ := storedString(t, srv, "xor")
	xorAgain, _ := storedString(t, srv, "xor_again")
	if xor != xorAgain {
		t.Errorf("a XOR b = %08b, but (a OR b) AND NOT (a AND b) = %08b", []byte(xor), []byte(xorAgain))
	}
}
