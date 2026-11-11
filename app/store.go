package main

import (
	"errors"
	"fmt"
	"time"
)

// noExpiry is the expiry time of keys that never expire.
var noExpiry time.Time

// errWrongType is returned when a command is run against a key holding a
// value of another type than the one the command works with, such as GET on
// a list. Its message is the error clients receive.
var errWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")

// entry is what the keyspace stores for a key.
type entry struct {
	// value is the key's value. Its dynamic type is the type of the key:
	// string for strings, *list for lists and *stream for streams.
	value any
	// expiresAt is the instant from which the key no longer exists. The zero
	// time means the key never expires.
	expiresAt time.Time
}

// typeName returns the name of the type of the entry's value, as reported by
// the TYPE command.
func (e entry) typeName() string {
	switch e.value.(type) {
	case string:
		return "string"
	case *list:
		return "list"
	case *stream:
		return "stream"
	default:
		panic(fmt.Sprintf("keyspace holds a value of unexpected type %T", e.value))
	}
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
	// onModify, if set, is called with the key every time a key is modified:
	// when its value is set, changed in place or deleted, expiry included.
	onModify func(key string)
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
		st.modified(key)
		return entry{}, false
	}
	return e, true
}

// modified reports that key was modified to whoever asked to be told. The
// store calls it itself when it sets or deletes a key; code that changes a
// value in place, such as a push to an existing list, has to call it.
func (st *store) modified(key string) {
	if st.onModify != nil {
		st.onModify(key)
	}
}

// delete removes key from the keyspace. Deleting a key that doesn't exist is
// a no-op.
func (st *store) delete(key string) {
	if _, ok := st.entries[key]; !ok {
		return
	}
	delete(st.entries, key)
	st.modified(key)
}

// valueAt returns the value stored at key if it is of type T, and whether the
// key exists. It fails with errWrongType if the key holds a value of another
// type.
func valueAt[T any](st *store, key string) (T, bool, error) {
	var none T
	e, ok := st.lookup(key)
	if !ok {
		return none, false, nil
	}
	value, ok := e.value.(T)
	if !ok {
		return none, false, errWrongType
	}
	return value, true, nil
}

// getString returns the string stored at key, and whether the key exists. It
// fails with errWrongType if the key holds something else than a string.
func (st *store) getString(key string) (string, bool, error) {
	return valueAt[string](st, key)
}

// setString stores a string at key, replacing any previous value (whatever
// its type) and expiry. The key stops existing at expiresAt; pass noExpiry
// for a key that never expires.
func (st *store) setString(key, value string, expiresAt time.Time) {
	st.entries[key] = entry{value: value, expiresAt: expiresAt}
	st.modified(key)
}

// getList returns the list stored at key, or nil if the key doesn't exist. It
// fails with errWrongType if the key holds something else than a list.
func (st *store) getList(key string) (*list, error) {
	l, _, err := valueAt[*list](st, key)
	return l, err
}

// getOrCreateList returns the list stored at key, creating it if the key
// doesn't exist. It fails with errWrongType if the key holds something else
// than a list.
//
// Lists never stay empty in the keyspace: callers must add at least one
// element to a list they created, and delete the key of a list they emptied.
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

// getStream returns the stream stored at key, or nil if the key doesn't
// exist. It fails with errWrongType if the key holds something else than a
// stream.
func (st *store) getStream(key string) (*stream, error) {
	stream, _, err := valueAt[*stream](st, key)
	return stream, err
}

// setStream stores a stream at key, replacing any previous value.
func (st *store) setStream(key string, value *stream) {
	st.entries[key] = entry{value: value}
	st.modified(key)
}
