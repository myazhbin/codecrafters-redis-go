package main

import (
	"crypto/rand"
	"net"
	"testing"
)

// expectBits checks that GETBIT reads the bytes of want, bit by bit, in the
// string stored at key, and nothing but zeros past its end.
func expectBits(t *testing.T, conn net.Conn, key, want string) {
	t.Helper()

	for i := 0; i < len(want); i++ {
		for bit := range 8 {
			expectBit(t, conn, key, 8*i+bit, int(want[i]>>(7-bit))&1)
		}
	}
	for offset := 8 * len(want); offset < 8*len(want)+16; offset++ {
		expectBit(t, conn, key, offset, 0)
	}
}

func TestGetBitOfAStringCreatedWithSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// "A" is the byte 01000001.
	do(t, conn, "SET", "mykey", "A")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "GETBIT", "mykey", "1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "mykey", "7")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GETBIT", "mykey", "0")
	expectReply(t, conn, ":0\r\n")
}

func TestGetBitReadsEveryBitOfAOneByteString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		value string
		bits  [8]int
	}{
		{"A", [8]int{0, 1, 0, 0, 0, 0, 0, 1}},
		{"a", [8]int{0, 1, 1, 0, 0, 0, 0, 1}},
		{"0", [8]int{0, 0, 1, 1, 0, 0, 0, 0}},
		{" ", [8]int{0, 0, 1, 0, 0, 0, 0, 0}},
		{"\x00", [8]int{0, 0, 0, 0, 0, 0, 0, 0}},
		{"\xff", [8]int{1, 1, 1, 1, 1, 1, 1, 1}},
		{"\x80", [8]int{1, 0, 0, 0, 0, 0, 0, 0}},
		{"\x01", [8]int{0, 0, 0, 0, 0, 0, 0, 1}},
	}
	for _, tt := range tests {
		do(t, conn, "SET", "mykey", tt.value)
		expectReply(t, conn, "+OK\r\n")
		for offset, want := range tt.bits {
			expectBit(t, conn, "mykey", offset, want)
		}
		// There is no second byte.
		expectBit(t, conn, "mykey", 8, 0)
	}
}

func TestGetBitOfALongerString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Bit 8 is the most significant bit of the second byte, and so on.
	do(t, conn, "SET", "mykey", "AB")
	expectReply(t, conn, "+OK\r\n")
	// 01000001 01000010
	for offset, want := range []int{0, 1, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 1, 0} {
		expectBit(t, conn, "mykey", offset, want)
	}

	do(t, conn, "SET", "greeting", "hello world")
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "greeting", "hello world")
}

func TestGetBitOfAnyBytes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// GETBIT reads the bytes of the string, whatever they are.
	for _, value := range []string{"\x00\xff\x0f\xf0", "a\r\nb", "héllo ✓", "*1\r\n$4\r\nPING\r\n", "\x00\x00\x00"} {
		do(t, conn, "SET", "mykey", value)
		expectReply(t, conn, "+OK\r\n")
		expectBits(t, conn, "mykey", value)
	}

	// Bytes that can't be hardcoded.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating random bytes: %v", err)
	}
	do(t, conn, "SET", "mykey", string(raw))
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "mykey", string(raw))
}

func TestGetBitOfAnEmptyString(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "mykey", "")
	expectReply(t, conn, "+OK\r\n")
	for _, offset := range []int{0, 1, 7, 8, 1000} {
		expectBit(t, conn, "mykey", offset, 0)
	}
	// The key exists, as an empty string.
	do(t, conn, "GET", "mykey")
	expectReply(t, conn, "$0\r\n\r\n")
}

func TestGetBitOfANumber(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A number is stored as its decimal digits: the bits are those of the
	// characters "4" and "2", not of the integer 42.
	do(t, conn, "SET", "counter", "42")
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "counter", "42")

	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":43\r\n")
	expectBits(t, conn, "counter", "43")
}

func TestGetBitFollowsSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// SET replaces the whole string: no bit of the previous one is left.
	do(t, conn, "SET", "mykey", "\xff\xff\xff")
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "mykey", "\xff\xff\xff")
	do(t, conn, "SET", "mykey", "A")
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "mykey", "A")

	// That goes for a bitmap made with SETBIT too.
	setBitTo(t, conn, "bitmap_key", 0, 1, 0)
	setBitTo(t, conn, "bitmap_key", 20, 1, 0)
	do(t, conn, "SET", "bitmap_key", "A")
	expectReply(t, conn, "+OK\r\n")
	expectBits(t, conn, "bitmap_key", "A")
}

func TestSetBitChangesAStringCreatedWithSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// SET and SETBIT write the same bytes: "A" is 01000001, and with bit 6
	// set it is 01000011.
	do(t, conn, "SET", "mykey", "A")
	expectReply(t, conn, "+OK\r\n")
	setBitTo(t, conn, "mykey", 6, 1, 0)
	setBitTo(t, conn, "mykey", 1, 1, 1)
	for offset, want := range []int{0, 1, 0, 0, 0, 0, 1, 1} {
		expectBit(t, conn, "mykey", offset, want)
	}
}
