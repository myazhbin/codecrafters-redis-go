package main

import "strconv"

// The helpers in this file encode replies using RESP, the Redis wire protocol.

// simpleString encodes a short, non binary-safe status reply such as "+OK".
func simpleString(s string) []byte {
	return []byte("+" + s + "\r\n")
}

// errorReply encodes an error. By convention msg starts with an upper-case
// error code, such as "ERR unknown command".
func errorReply(msg string) []byte {
	return []byte("-" + msg + "\r\n")
}

// integerReply encodes a signed integer, such as ":42".
func integerReply(n int) []byte {
	return []byte(":" + strconv.Itoa(n) + "\r\n")
}

// bulkString encodes a binary-safe string, prefixed by its length in bytes.
func bulkString(s string) []byte {
	b := make([]byte, 0, len(s)+16)
	b = append(b, '$')
	b = strconv.AppendInt(b, int64(len(s)), 10)
	b = append(b, '\r', '\n')
	b = append(b, s...)
	return append(b, '\r', '\n')
}

// nullBulkString is the reply for a string that doesn't exist. It is distinct
// from the empty string, which is encoded as "$0\r\n\r\n".
var nullBulkString = []byte("$-1\r\n")

// bulkStringArray encodes an array whose elements are all bulk strings. An
// empty (or nil) slice is encoded as the empty array, "*0\r\n".
func bulkStringArray(items []string) []byte {
	b := make([]byte, 0, 16+16*len(items))
	b = append(b, '*')
	b = strconv.AppendInt(b, int64(len(items)), 10)
	b = append(b, '\r', '\n')
	for _, item := range items {
		b = append(b, bulkString(item)...)
	}
	return b
}
