package main

// store is the keyspace: it holds every key of the server along with its
// value.
//
// A store is not safe for concurrent use. The server only touches it while
// holding its lock (see Server.mu).
type store struct {
	strings map[string]string
}

func newStore() *store {
	return &store{strings: make(map[string]string)}
}

// get returns the string stored at key, and whether the key exists.
func (st *store) get(key string) (string, bool) {
	value, ok := st.strings[key]
	return value, ok
}

// set stores value at key, replacing any previous value.
func (st *store) set(key, value string) {
	st.strings[key] = value
}
