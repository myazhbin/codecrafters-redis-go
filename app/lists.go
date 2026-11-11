package main

import "strconv"

// This file implements the commands that operate on lists.

// cmdRPush implements RPUSH key element [element ...]: it appends the
// elements to the list stored at key, creating the list if needed, and
// replies with the new length of the list.
func cmdRPush(s *Server, args []string) []byte {
	l, err := s.store.getOrCreateList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	l.items = append(l.items, args[1:]...)
	return integerReply(len(l.items))
}

// cmdLRange implements LRANGE key start stop: it replies with the elements of
// the list stored at key from index start to index stop, both included.
// Negative indexes count from the end of the list. A list that doesn't exist
// is treated as an empty one.
func cmdLRange(s *Server, args []string) []byte {
	start, startErr := strconv.Atoi(args[1])
	stop, stopErr := strconv.Atoi(args[2])
	if startErr != nil || stopErr != nil {
		return errorReply("ERR value is not an integer or out of range")
	}

	l, err := s.store.getList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if l == nil {
		return bulkStringArray(nil)
	}
	return bulkStringArray(l.slice(start, stop))
}
