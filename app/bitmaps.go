package main

import "strings"

// This file implements bitmaps and the commands that operate on them.
//
// A bitmap isn't a type of its own: it is a string, seen as a sequence of
// bits rather than bytes. Bits are numbered from the left: bit 0 is the most
// significant bit of the first byte, bit 7 the least significant one, bit 8
// the most significant bit of the second byte, and so on.

// maxBitOffset is the offset no bit can be at or beyond: strings are limited
// to 512MB, which is 2^32 bits.
const maxBitOffset = 8 * maxBulkLength

// parseBitOffset parses the offset of a bit in a bitmap.
func parseBitOffset(s string) (int64, bool) {
	offset, ok := parseInt(s)
	if !ok || offset < 0 || offset >= maxBitOffset {
		return 0, false
	}
	return offset, true
}

// bitPosition returns where the bit at offset is in a string: the index of
// its byte and its mask in that byte.
func bitPosition(offset int64) (index int, mask byte) {
	return int(offset / 8), 0x80 >> (offset % 8)
}

// getBit returns the bit at offset in value. A string has no bit beyond its
// last byte: those bits are 0.
func getBit(value string, offset int64) byte {
	index, mask := bitPosition(offset)
	if index < len(value) && value[index]&mask != 0 {
		return 1
	}
	return 0
}

// zeros is a block of zero bytes, to pad strings with.
var zeros [4096]byte

// setBit returns value with the bit at offset set to bit, which is 0 or 1,
// along with what the bit was before. A string that is too short to have a
// bit at offset is extended with zero bytes: the bits it doesn't have are 0.
// changed tells whether the result differs from value at all.
//
// Strings can't be modified in place: the result is a copy of value, which
// makes setting a bit as expensive as the bitmap is long.
func setBit(value string, offset int64, bit byte) (updated string, old byte, changed bool) {
	index, mask := bitPosition(offset)

	var current byte
	if index < len(value) {
		current = value[index]
	}
	if current&mask != 0 {
		old = 1
	}
	replacement := current &^ mask
	if bit == 1 {
		replacement |= mask
	}
	if index < len(value) && replacement == current {
		return value, old, false
	}

	var b strings.Builder
	b.Grow(max(len(value), index+1))
	if index < len(value) {
		b.WriteString(value[:index])
		b.WriteByte(replacement)
		b.WriteString(value[index+1:])
	} else {
		b.WriteString(value)
		for padding := index - len(value); padding > 0; {
			n := min(padding, len(zeros))
			b.Write(zeros[:n])
			padding -= n
		}
		b.WriteByte(replacement)
	}
	return b.String(), old, true
}

// cmdSetBit implements SETBIT key offset value: it sets the bit at offset in
// the string stored at key to value, which is 0 or 1, and replies with what
// the bit was before.
//
// A key that doesn't exist is created, and a string that is too short is
// extended, with zero bytes up to the one that holds the bit. The expiry of
// the key, if any, is left untouched.
func cmdSetBit(c *client, args []string) []byte {
	s := c.server
	key := args[0]

	offset, ok := parseBitOffset(args[1])
	if !ok {
		return errorReply("ERR bit offset is not an integer or out of range")
	}
	var bit byte
	switch args[2] {
	case "0":
	case "1":
		bit = 1
	default:
		return errorReply("ERR bit is not an integer or out of range")
	}

	value, expiresAt := "", noExpiry
	if e, exists := s.store.lookup(key); exists {
		str, isString := e.value.(string)
		if !isString {
			return errorReply(errWrongType.Error())
		}
		value, expiresAt = str, e.expiresAt
	}

	// Setting a bit to the value it has, in a string that is long enough,
	// isn't a modification: there is nothing to store or to propagate.
	updated, old, changed := setBit(value, offset, bit)
	if changed {
		s.store.setString(key, updated, expiresAt)
		s.propagate(append([]string{"SETBIT"}, args...)...)
	}
	return integerReply(int(old))
}

// cmdGetBit implements GETBIT key offset: it replies with the bit at offset
// in the string stored at key. The bits that the string is too short to have
// are 0, and so are all the bits of a key that doesn't exist.
func cmdGetBit(c *client, args []string) []byte {
	offset, ok := parseBitOffset(args[1])
	if !ok {
		return errorReply("ERR bit offset is not an integer or out of range")
	}

	value, _, err := c.server.store.getString(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	return integerReply(int(getBit(value, offset)))
}
