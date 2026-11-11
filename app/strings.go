package main

// This file implements the commands that operate on string values.

// cmdSet implements SET key value: it stores value at key, overwriting
// whatever the key held before.
func cmdSet(s *Server, args []string) []byte {
	if len(args) > 2 {
		return errorReply("ERR syntax error")
	}
	s.store.set(args[0], args[1])
	return simpleString("OK")
}

// cmdGet implements GET key: it replies with the value stored at key, or a
// null bulk string if the key doesn't exist.
func cmdGet(s *Server, args []string) []byte {
	value, ok := s.store.get(args[0])
	if !ok {
		return nullBulkString
	}
	return bulkString(value)
}
