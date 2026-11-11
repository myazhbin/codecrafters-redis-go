package main

// This file implements transactions: MULTI and the commands that go with it.

// cmdMulti implements MULTI: it starts a transaction on the client's
// connection. From then on the client's commands are queued instead of being
// executed. Transactions can't be nested.
func cmdMulti(c *client, args []string) []byte {
	if c.inTransaction {
		return errorReply("ERR MULTI calls can not be nested")
	}
	c.inTransaction = true
	return simpleString("OK")
}

// cmdExec implements EXEC: it executes the commands queued since MULTI, in
// the order they were queued, and replies with an array holding the reply of
// each of them. It is an error to call it outside of a transaction.
//
// If a key watched with WATCH was modified in the meantime, the transaction
// is aborted: none of the commands is executed and the reply is a null array.
//
// The transaction is atomic: the server's lock is held from the first command
// to the last, so no other client can see, or interfere with, a state in
// which only some of the commands have been executed.
func cmdExec(c *client, args []string) []byte {
	if !c.inTransaction {
		return errorReply("ERR EXEC without MULTI")
	}
	queued := c.queued
	c.inTransaction = false
	c.queued = nil

	// EXEC ends the watch whatever the outcome. It has to happen before the
	// transaction runs: its own writes must not be taken for someone else's.
	aborted := c.watchedKeysModified()
	c.unwatch()
	if aborted {
		return nullArray
	}

	c.executing = true
	defer func() { c.executing = false }()

	replies := make([][]byte, len(queued))
	for i, q := range queued {
		replies[i] = q.cmd.handler(c, q.args)
	}
	return arrayReply(replies...)
}

// cmdDiscard implements DISCARD: it aborts the transaction started by MULTI,
// dropping the queued commands without executing any of them. The connection
// is back to normal afterwards, and no longer watches any key. It is an error to call it outside of a
// transaction.
func cmdDiscard(c *client, args []string) []byte {
	if !c.inTransaction {
		return errorReply("ERR DISCARD without MULTI")
	}
	c.inTransaction = false
	c.queued = nil
	// Like EXEC, DISCARD ends the watch along with the transaction.
	c.unwatch()
	return simpleString("OK")
}

// cmdWatch implements WATCH key [key ...]: it asks the server to monitor the
// keys for changes, so that the client's next transaction is made conditional
// on them (optimistic locking): if any of the keys is modified between WATCH
// and EXEC, by any client, the transaction is aborted.
//
// WATCH comes before the transaction it protects: calling it once MULTI has
// started one is an error. The transaction itself goes on unaffected.
func cmdWatch(c *client, args []string) []byte {
	if c.inTransaction {
		return errorReply("ERR WATCH inside MULTI is not allowed")
	}
	for _, key := range args {
		c.watch(key)
	}
	return simpleString("OK")
}

// cmdUnwatch implements UNWATCH: it makes the client stop watching all of the
// keys it watches, if any. Modifications made to them, before or after, don't
// abort the client's next transaction anymore.
func cmdUnwatch(c *client, args []string) []byte {
	c.unwatch()
	return simpleString("OK")
}

// watch makes the client watch key. Watching a key that is already watched
// changes nothing: modifications seen since the first WATCH still count.
func (c *client) watch(key string) {
	if _, ok := c.watched[key]; ok {
		return
	}
	// A key that is past its expiry time is removed now, so that its removal
	// isn't mistaken later for a modification made while it was watched.
	c.server.store.lookup(key)

	if c.watched == nil {
		c.watched = make(map[string]struct{})
	}
	c.watched[key] = struct{}{}

	watchers := c.server.watchers[key]
	if watchers == nil {
		watchers = make(map[*client]struct{})
		c.server.watchers[key] = watchers
	}
	watchers[c] = struct{}{}
}

// unwatch makes the client stop watching all of its keys and forgets about
// the modifications seen so far.
func (c *client) unwatch() {
	for key := range c.watched {
		watchers := c.server.watchers[key]
		delete(watchers, c)
		if len(watchers) == 0 {
			delete(c.server.watchers, key)
		}
	}
	c.watched = nil
	c.dirty = false
}

// watchedKeysModified reports whether one of the keys the client watches has
// been modified since it started watching it.
func (c *client) watchedKeysModified() bool {
	// Keys expire lazily: looking the watched keys up makes the ones whose
	// time has come go away, which counts as a modification.
	for key := range c.watched {
		if c.dirty {
			break
		}
		c.server.store.lookup(key)
	}
	return c.dirty
}

// keyModified is called whenever a key is modified. It flags the clients
// watching the key: what matters is that the key was touched, not whether
// its value ends up being different.
func (s *Server) keyModified(key string) {
	for c := range s.watchers[key] {
		c.dirty = true
	}
}
