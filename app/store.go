package main

import (
	"errors"
	"time"
)

// noExpiry is the expiry time of keys that never expire.
var noExpiry time.Time

// errWrongType is returned when a command is run against a key holding a
// value of another type than the one the command works with, such as GET on
// a list. Its message is the error clients receive.
var errWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")

// list is the value of a list key: an ordered sequence of strings.
type list struct {
	items []string
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

// entry is what the keyspace stores for a key.
type entry struct {
	// value is the key's value. Its dynamic type is the type of the key:
	// string for strings and *list for lists.
	value any
	// expiresAt is the instant from which the key no longer exists. The zero
	// time means the key never expires.
	expiresAt time.Time
}

// expired reports whether the entry is past its expiry time.
func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && !now.Before(e.expiresAt)
}

// store is the keyspace: it holds every key of the server along with its
// value.
//
// Expired keys are removed lazily: they stay in memory until something tries
// to access them, at which point they're deleted and reported as missing.
//
// A store is not safe for concurrent use. The server only touches it while
// holding its lock (see Server.mu).
type store struct {
	entries map[string]entry
	// now returns the current time. It's a field so that tests can control
	// the clock instead of sleeping.
	now func() time.Time
}

func newStore() *store {
	return &store{
		entries: make(map[string]entry),
		now:     time.Now,
	}
}

// lookup returns the entry stored at key, and whether the key exists. Keys
// that are past their expiry time don't exist anymore.
func (st *store) lookup(key string) (entry, bool) {
	e, ok := st.entries[key]
	if !ok {
		return entry{}, false
	}
	if e.expired(st.now()) {
		delete(st.entries, key)
		return entry{}, false
	}
	return e, true
}

// getString returns the string stored at key, and whether the key exists. It
// fails with errWrongType if the key holds something else than a string.
func (st *store) getString(key string) (string, bool, error) {
	e, ok := st.lookup(key)
	if !ok {
		return "", false, nil
	}
	value, ok := e.value.(string)
	if !ok {
		return "", false, errWrongType
	}
	return value, true, nil
}

// setString stores a string at key, replacing any previous value (whatever
// its type) and expiry. The key stops existing at expiresAt; pass noExpiry
// for a key that never expires.
func (st *store) setString(key, value string, expiresAt time.Time) {
	st.entries[key] = entry{value: value, expiresAt: expiresAt}
}

// getList returns the list stored at key, or nil if the key doesn't exist. It
// fails with errWrongType if the key holds something else than a list.
func (st *store) getList(key string) (*list, error) {
	e, ok := st.lookup(key)
	if !ok {
		return nil, nil
	}
	l, ok := e.value.(*list)
	if !ok {
		return nil, errWrongType
	}
	return l, nil
}

// getOrCreateList returns the list stored at key, creating it if the key
// doesn't exist. It fails with errWrongType if the key holds something else
// than a list.
//
// Callers must add at least one element to a list they created: empty lists
// are not allowed to stay in the keyspace.
func (st *store) getOrCreateList(key string) (*list, error) {
	l, err := st.getList(key)
	if err != nil {
		return nil, err
	}
	if l == nil {
		l = &list{}
		st.entries[key] = entry{value: l}
	}
	return l, nil
}
