package main

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// This file implements the commands that operate on string values.

// cmdSet implements SET key value [EX seconds | PX milliseconds]: it stores
// value at key, overwriting whatever the key held before, including its
// expiry.
func cmdSet(c *client, args []string) []byte {
	store := c.server.store
	key, value := args[0], args[1]

	// Options are parsed in full before any of their values is validated, so
	// that a malformed command is always reported as a syntax error.
	var unit, ttl string
	for i := 2; i < len(args); i++ {
		switch option := strings.ToUpper(args[i]); option {
		case "EX", "PX":
			if unit != "" || i+1 >= len(args) {
				return errorReply("ERR syntax error")
			}
			unit, ttl = option, args[i+1]
			i++
		default:
			return errorReply("ERR syntax error")
		}
	}

	expiresAt := noExpiry
	if unit != "" {
		var errReply []byte
		expiresAt, errReply = parseExpiry(unit, ttl, store.now())
		if errReply != nil {
			return errReply
		}
	}

	store.setString(key, value, expiresAt)
	c.server.propagate(append([]string{"SET"}, args...)...)
	return simpleString("OK")
}

// parseExpiry converts the time to live given to SET into the instant the
// key expires at. unit is "EX" (seconds) or "PX" (milliseconds). On invalid
// input it returns the error reply to send instead.
func parseExpiry(unit, ttl string, now time.Time) (time.Time, []byte) {
	n, ok := parseInt(ttl)
	if !ok {
		return noExpiry, errorReply(errNotAnInteger)
	}

	invalid := errorReply("ERR invalid expire time in 'set' command")
	if n <= 0 {
		return noExpiry, invalid
	}
	if unit == "EX" {
		if n > math.MaxInt64/1000 {
			return noExpiry, invalid
		}
		n *= 1000
	}
	// n is now in milliseconds. Working with absolute Unix milliseconds
	// (rather than a time.Duration) keeps very long expiries from overflowing.
	nowMs := now.UnixMilli()
	if n > math.MaxInt64-nowMs {
		return noExpiry, invalid
	}
	return time.UnixMilli(nowMs + n), nil
}

// cmdGet implements GET key: it replies with the string stored at key, or a
// null bulk string if the key doesn't exist.
func cmdGet(c *client, args []string) []byte {
	value, ok, err := c.server.store.getString(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if !ok {
		return nullBulkString
	}
	return bulkString(value)
}

// cmdStrlen implements STRLEN key: it replies with the length of the string
// stored at key, in bytes, which is 0 if the key doesn't exist.
func cmdStrlen(c *client, args []string) []byte {
	value, _, err := c.server.store.getString(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	return integerReply(len(value))
}

// cmdIncr implements INCR key: it adds one to the integer stored at key and
// replies with the new value. A key that doesn't exist counts as 0, so its
// value becomes 1.
//
// The value is a string holding the decimal representation of a signed 64-bit
// integer. The expiry of the key, if any, is left untouched.
func cmdIncr(c *client, args []string) []byte {
	store := c.server.store
	key := args[0]

	var n int64
	expiresAt := noExpiry
	if e, exists := store.lookup(key); exists {
		value, isString := e.value.(string)
		if !isString {
			return errorReply(errWrongType.Error())
		}
		var isInteger bool
		if n, isInteger = parseInt(value); !isInteger {
			return errorReply(errNotAnInteger)
		}
		expiresAt = e.expiresAt
	}

	if n == math.MaxInt64 {
		return errorReply("ERR increment or decrement would overflow")
	}
	n++
	store.setString(key, strconv.FormatInt(n, 10), expiresAt)
	c.server.propagate("INCR", key)
	return integerReply(n)
}
