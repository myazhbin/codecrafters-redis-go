package main

import "strconv"

// This file implements lists and the commands that operate on them.

// list is the value of a list key: an ordered sequence of strings.
type list struct {
	items []string
}

// pushBack appends the elements at the end (the "right") of the list, in the
// order they are given.
func (l *list) pushBack(elements ...string) {
	l.items = append(l.items, elements...)
}

// pushFront inserts the elements at the start (the "left") of the list, one
// after another. Each element goes in front of the previous one, so the last
// element given ends up being the first of the list.
func (l *list) pushFront(elements ...string) {
	items := make([]string, 0, len(elements)+len(l.items))
	for i := len(elements) - 1; i >= 0; i-- {
		items = append(items, elements[i])
	}
	l.items = append(items, l.items...)
}

// slice returns the elements of the list from index start to index stop,
// both included.
//
// Indexes start at 0. Negative indexes count from the end of the list: -1 is
// the last element, -2 the one before, and so on. Indexes that fall outside
// of the list are brought back to its closest end, and a range that covers no
// element (such as one starting after its own stop) is empty.
//
// The result shares its memory with the list and must not be modified.
func (l *list) slice(start, stop int) []string {
	n := len(l.items)
	if start < 0 {
		start = max(start+n, 0)
	}
	if stop < 0 {
		stop += n
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop {
		return nil
	}
	return l.items[start : stop+1]
}

// cmdRPush implements RPUSH key element [element ...]: it appends the
// elements to the list stored at key, creating the list if needed, and
// replies with the new length of the list.
func cmdRPush(s *Server, args []string) []byte {
	l, err := s.store.getOrCreateList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	l.pushBack(args[1:]...)
	return integerReply(len(l.items))
}

// cmdLPush implements LPUSH key element [element ...]: it inserts the
// elements one by one at the head of the list stored at key, creating the
// list if needed, and replies with the new length of the list.
//
// Since each element is inserted in front of the previous one,
// "LPUSH key a b c" on a new key produces the list [c b a].
func cmdLPush(s *Server, args []string) []byte {
	l, err := s.store.getOrCreateList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	l.pushFront(args[1:]...)
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
