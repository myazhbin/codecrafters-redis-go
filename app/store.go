package main

import "time"

// noExpiry is the expiry time of keys that never expire.
var noExpiry time.Time

// entry is what the keyspace stores for a key.
type entry struct {
	value string
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

// get returns the string stored at key, and whether the key exists.
func (st *store) get(key string) (string, bool) {
	e, ok := st.entries[key]
	if !ok {
		return "", false
	}
	if e.expired(st.now()) {
		delete(st.entries, key)
		return "", false
	}
	return e.value, true
}

// set stores value at key, replacing any previous value and expiry. The key
// stops existing at expiresAt; pass noExpiry for a key that never expires.
func (st *store) set(key, value string, expiresAt time.Time) {
	st.entries[key] = entry{value: value, expiresAt: expiresAt}
}
