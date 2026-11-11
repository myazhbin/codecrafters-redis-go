package main

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
