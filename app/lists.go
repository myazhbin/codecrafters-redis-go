package main

import (
	"math"
	"strconv"
	"time"
)

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

// popFront removes up to count elements from the start of the list and
// returns them, first element first. Fewer elements are returned if the list
// is shorter than count.
func (l *list) popFront(count int) []string {
	count = min(max(count, 0), len(l.items))

	popped := make([]string, count)
	copy(popped, l.items[:count])

	// Clear the slots that are left behind so the popped strings can be
	// garbage collected even though the backing array lives on.
	clear(l.items[:count])
	l.items = l.items[count:]
	return popped
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

// popFromList removes and returns the first element of the list l stored at
// key, deleting the key if that leaves the list empty.
func (s *Server) popFromList(key string, l *list) string {
	element := l.popFront(1)[0]
	if len(l.items) == 0 {
		s.store.delete(key)
	}
	return element
}

// listWaiter is a client blocked in BLPOP, waiting for an element to be
// pushed to one of several lists.
type listWaiter struct {
	// keys are the lists the client waits on, without duplicates.
	keys []string
	// served receives the element that was popped on behalf of the client.
	// It is buffered so that handing over an element never blocks.
	served chan poppedElement
}

// poppedElement is an element removed from the list stored at key.
type poppedElement struct {
	key, element string
}

// addListWaiter registers a client as waiting for an element on any of the
// keys, behind the clients that are already waiting on them.
func (s *Server) addListWaiter(keys []string) *listWaiter {
	w := &listWaiter{keys: unique(keys), served: make(chan poppedElement, 1)}
	s.listWaiters.add(w, w.keys)
	return w
}

// removeListWaiter makes w stop waiting on all of its keys.
func (s *Server) removeListWaiter(w *listWaiter) {
	s.listWaiters.remove(w, w.keys)
}

// serveListWaiters hands the elements of the list stored at key over to the
// clients blocked on that key, one element each, starting with the client
// that has been waiting the longest. It must be called whenever elements are
// added to a list, so that a list never holds elements while clients wait
// for them.
func (s *Server) serveListWaiters(key string) {
	for len(s.listWaiters[key]) > 0 {
		l, err := s.store.getList(key)
		if err != nil || l == nil {
			return
		}
		w := s.listWaiters[key][0]
		s.removeListWaiter(w)
		w.served <- poppedElement{key: key, element: s.popFromList(key, l)}
	}
}

// cmdRPush implements RPUSH key element [element ...]: it appends the
// elements to the list stored at key, creating the list if needed, and
// replies with the new length of the list.
func cmdRPush(c *client, args []string) []byte {
	return pushCommand(c, args, (*list).pushBack)
}

// cmdLPush implements LPUSH key element [element ...]: it inserts the
// elements one by one at the head of the list stored at key, creating the
// list if needed, and replies with the new length of the list.
//
// Since each element is inserted in front of the previous one,
// "LPUSH key a b c" on a new key produces the list [c b a].
func cmdLPush(c *client, args []string) []byte {
	return pushCommand(c, args, (*list).pushFront)
}

// pushCommand implements RPUSH and LPUSH, which only differ by the end of the
// list that push adds the elements to.
func pushCommand(c *client, args []string, push func(l *list, elements ...string)) []byte {
	s := c.server
	key := args[0]

	l, err := s.store.getOrCreateList(key)
	if err != nil {
		return errorReply(err.Error())
	}
	push(l, args[1:]...)

	// The reply is the length of the list right after the push, even if some
	// of the new elements are immediately taken by blocked clients.
	reply := integerReply(len(l.items))
	s.serveListWaiters(key)
	return reply
}

// cmdLPop implements LPOP key [count]: it removes elements from the start of
// the list stored at key and replies with them. A list that loses its last
// element is deleted.
//
// Without count, a single element is removed and sent as a bulk string, or a
// null bulk string if the key doesn't exist. With count, up to count elements
// are removed and sent as an array, or a null array if the key doesn't exist.
func cmdLPop(c *client, args []string) []byte {
	if len(args) > 2 {
		return wrongArgumentCountError("LPOP")
	}
	s := c.server
	key := args[0]

	hasCount := len(args) == 2
	count := 1
	if hasCount {
		var err error
		count, err = strconv.Atoi(args[1])
		if err != nil || count < 0 {
			return errorReply("ERR value is out of range, must be positive")
		}
	}

	l, err := s.store.getList(key)
	if err != nil {
		return errorReply(err.Error())
	}
	if l == nil {
		if hasCount {
			return nullArray
		}
		return nullBulkString
	}

	if !hasCount {
		return bulkString(s.popFromList(key, l))
	}
	popped := l.popFront(count)
	if len(l.items) == 0 {
		s.store.delete(key)
	}
	return bulkStringArray(popped)
}

// maxBlockingTimeout is the longest timeout, in seconds, a blocking command
// accepts: anything above doesn't fit in a time.Duration.
const maxBlockingTimeout = float64(math.MaxInt64 / int64(time.Second))

// cmdBLPop implements BLPOP key [key ...] timeout: it removes the first
// element of the first of the given lists that has one, and replies with an
// array holding the key of that list and the element.
//
// If all the lists are empty, the client is blocked until another client
// pushes an element to one of them. When several clients are blocked on the
// same list, the one that has been waiting the longest is served first.
//
// timeout is the maximum number of seconds to wait, and can have a fractional
// part. If no element shows up in time the reply is a null array. A timeout
// of 0 means "wait for as long as it takes".
func cmdBLPop(c *client, args []string) []byte {
	s := c.server
	keys, rawTimeout := args[:len(args)-1], args[len(args)-1]

	timeout, err := strconv.ParseFloat(rawTimeout, 64)
	if err != nil || math.IsNaN(timeout) || math.IsInf(timeout, 0) {
		return errorReply("ERR timeout is not a float or out of range")
	}
	if timeout < 0 {
		return errorReply("ERR timeout is negative")
	}
	if timeout > maxBlockingTimeout {
		return errorReply("ERR timeout is out of range")
	}

	// If one of the lists already has an element there's no need to wait.
	for _, key := range keys {
		l, err := s.store.getList(key)
		if err != nil {
			return errorReply(err.Error())
		}
		if l != nil {
			return bulkStringArray([]string{key, s.popFromList(key, l)})
		}
	}

	// A nil channel is never ready: without timeout the wait has no deadline.
	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(time.Duration(timeout * float64(time.Second)))
		defer timer.Stop()
		expired = timer.C
	}

	// Wait for an element without holding the lock, so that other clients
	// get a chance to push one.
	w := s.addListWaiter(keys)
	s.mu.Unlock()
	var (
		popped       poppedElement
		served       bool
		disconnected bool
	)
	select {
	case popped = <-w.served:
		served = true
	case <-expired:
	case <-c.ctx.Done():
		disconnected = true
	}
	s.mu.Lock()

	if !served {
		// The wait was given up, but an element may still have been handed
		// over before the lock was taken back.
		select {
		case popped = <-w.served:
			served = true
		default:
			s.removeListWaiter(w)
		}
	}

	switch {
	case served && disconnected:
		// Nobody is there to receive the element anymore: it goes back to
		// the head of its list, for the next client.
		if l, err := s.store.getOrCreateList(popped.key); err == nil {
			l.pushFront(popped.element)
			s.serveListWaiters(popped.key)
		}
		return nil
	case served:
		return bulkStringArray([]string{popped.key, popped.element})
	case disconnected:
		return nil
	default:
		return nullArray // timed out
	}
}

// cmdLLen implements LLEN key: it replies with the number of elements of the
// list stored at key, which is 0 if the key doesn't exist.
func cmdLLen(c *client, args []string) []byte {
	l, err := c.server.store.getList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if l == nil {
		return integerReply(0)
	}
	return integerReply(len(l.items))
}

// cmdLRange implements LRANGE key start stop: it replies with the elements of
// the list stored at key from index start to index stop, both included.
// Negative indexes count from the end of the list. A list that doesn't exist
// is treated as an empty one.
func cmdLRange(c *client, args []string) []byte {
	start, startErr := strconv.Atoi(args[1])
	stop, stopErr := strconv.Atoi(args[2])
	if startErr != nil || stopErr != nil {
		return errorReply("ERR value is not an integer or out of range")
	}

	l, err := c.server.store.getList(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if l == nil {
		return bulkStringArray(nil)
	}
	return bulkStringArray(l.slice(start, stop))
}
