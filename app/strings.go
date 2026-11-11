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
func cmdSet(s *Server, args []string) []byte {
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
		expiresAt, errReply = parseExpiry(unit, ttl, s.store.now())
		if errReply != nil {
			return errReply
		}
	}

	s.store.setString(key, value, expiresAt)
	return simpleString("OK")
}

// parseExpiry converts the time to live given to SET into the instant the
// key expires at. unit is "EX" (seconds) or "PX" (milliseconds). On invalid
// input it returns the error reply to send instead.
func parseExpiry(unit, ttl string, now time.Time) (time.Time, []byte) {
	n, err := strconv.ParseInt(ttl, 10, 64)
	if err != nil {
		return noExpiry, errorReply("ERR value is not an integer or out of range")
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
func cmdGet(s *Server, args []string) []byte {
	value, ok, err := s.store.getString(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if !ok {
		return nullBulkString
	}
	return bulkString(value)
}
