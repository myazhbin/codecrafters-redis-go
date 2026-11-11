package main

import (
	"math/bits"
	"strings"
)

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

// popCount returns the number of bits that are set to 1 in s.
func popCount(s string) int {
	count := 0
	// Eight bytes at a time, for as long as there are that many.
	for len(s) >= 8 {
		word := uint64(s[0]) | uint64(s[1])<<8 | uint64(s[2])<<16 | uint64(s[3])<<24 |
			uint64(s[4])<<32 | uint64(s[5])<<40 | uint64(s[6])<<48 | uint64(s[7])<<56
		count += bits.OnesCount64(word)
		s = s[8:]
	}
	for i := 0; i < len(s); i++ {
		count += bits.OnesCount8(s[i])
	}
	return count
}

// countBits returns the number of bits set to 1 among the bits of value whose
// offset goes from first to last, both included. The two must be offsets of
// bits that value has, in that order.
func countBits(value string, first, last int64) int {
	from, to := int(first/8), int(last/8)
	// What is left of the first byte once the bits before first are masked,
	// and of the last byte once the bits after last are.
	head := value[from] & (0xFF >> (first % 8))
	lastMask := byte(0xFF << (7 - last%8))
	if from == to {
		return bits.OnesCount8(head & lastMask)
	}
	return bits.OnesCount8(head) + popCount(value[from+1:to]) + bits.OnesCount8(value[to]&lastMask)
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

// cmdBitCount implements BITCOUNT key [start end [BYTE | BIT]]: it replies
// with the number of bits set to 1 in the string stored at key, which is 0 if
// the key doesn't exist.
//
// With start and end, only the bits from index start to index end, both
// included, are counted. The indexes are those of bytes, unless BIT asks for
// them to be those of bits. They start at 0, and negative ones count from the
// end of the string: -1 is its last byte, or bit. An index that falls outside
// of the string is brought back to its closest end, and nothing is counted in
// a range whose start comes after its end.
func cmdBitCount(c *client, args []string) []byte {
	var (
		ranged     bool
		start, end int64
		inBits     bool
	)
	switch len(args) {
	case 1:
	case 3, 4:
		var startOK, endOK bool
		start, startOK = parseInt(args[1])
		end, endOK = parseInt(args[2])
		if !startOK || !endOK {
			return errorReply(errNotAnInteger)
		}
		if len(args) == 4 {
			switch strings.ToUpper(args[3]) {
			case "BIT":
				inBits = true
			case "BYTE":
			default:
				return errorReply("ERR syntax error")
			}
		}
		ranged = true
	default:
		return errorReply("ERR syntax error")
	}

	value, _, err := c.server.store.getString(args[0])
	if err != nil {
		return errorReply(err.Error())
	}

	// The number of things the indexes count: bytes, or bits.
	total := int64(len(value))
	if inBits {
		total *= 8
	}
	if !ranged {
		start, end = 0, total-1
	} else {
		// Two indexes that count from the end are compared before they are
		// brought back into the string, where they could both end up at 0.
		if start < 0 && end < 0 && start > end {
			return integerReply(0)
		}
		if start < 0 {
			start = max(start+total, 0)
		}
		if end < 0 {
			end = max(end+total, 0)
		}
		end = min(end, total-1)
	}
	if start > end {
		return integerReply(0)
	}
	if !inBits {
		start, end = 8*start, 8*end+7
	}
	return integerReply(countBits(value, start, end))
}

// bitOperations are the operations of BITOP that combine several strings, by
// name. Each of them combines, in place, the result so far with one more
// string, which is at most as long as the result: the bytes it doesn't have
// count as zeros.
//
// The strings are read byte by byte: ranging over them would go rune by rune.
var bitOperations = map[string]func(result []byte, source string){
	"AND": func(result []byte, source string) {
		for i := 0; i < len(source); i++ {
			result[i] &= source[i]
		}
		// The zeros the string is extended with clear what is left.
		clear(result[len(source):])
	},
	"OR": func(result []byte, source string) {
		for i := 0; i < len(source); i++ {
			result[i] |= source[i]
		}
	},
	"XOR": func(result []byte, source string) {
		for i := 0; i < len(source); i++ {
			result[i] ^= source[i]
		}
	},
}

// cmdBitOp implements BITOP operation destkey key [key ...]: it combines the
// strings stored at the keys, bit by bit, and stores the result at destkey,
// replacing whatever it held. The reply is the length of the result, in
// bytes.
//
// The operation is one of:
//
//   - AND: a bit of the result is 1 when that bit is 1 in every one of the
//     strings;
//   - OR: a bit of the result is 1 when that bit is 1 in at least one of the
//     strings;
//   - XOR: a bit of the result is 1 when that bit is 1 in an odd number of
//     the strings;
//   - NOT, which takes a single key: a bit of the result is 1 when that bit
//     is 0 in the string.
//
// The result is as long as the longest of the strings. The shorter ones, and
// the keys that don't exist, count as if they were extended with zero bytes.
// If all of the strings are empty, so is the result: there is nothing to
// store, and destkey is deleted instead.
func cmdBitOp(c *client, args []string) []byte {
	s := c.server
	operation, dest, keys := strings.ToUpper(args[0]), args[1], args[2:]
	combine, ok := bitOperations[operation]
	switch {
	case operation == "NOT":
		if len(keys) != 1 {
			return errorReply("ERR BITOP NOT must be called with a single source key.")
		}
	case !ok:
		return errorReply("ERR syntax error")
	}

	// Every key is read before anything is written: destkey can be one of
	// them.
	sources := make([]string, len(keys))
	length := 0
	for i, key := range keys {
		value, _, err := s.store.getString(key)
		if err != nil {
			return errorReply(err.Error())
		}
		sources[i] = value
		length = max(length, len(value))
	}

	if length == 0 {
		if _, exists := s.store.lookup(dest); exists {
			s.store.delete(dest)
			s.propagate(append([]string{"BITOP"}, args...)...)
		}
		return integerReply(0)
	}

	// The result starts as the first string, extended with zeros, and is
	// combined with each of the others in turn.
	result := make([]byte, length)
	copy(result, sources[0])
	if operation == "NOT" {
		for i := range result {
			result[i] = ^result[i]
		}
	}
	for _, source := range sources[1:] {
		combine(result, source)
	}

	s.store.setString(dest, string(result), noExpiry)
	s.propagate(append([]string{"BITOP"}, args...)...)
	return integerReply(length)
}
