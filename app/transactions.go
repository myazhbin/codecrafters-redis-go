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

	c.executing = true
	defer func() { c.executing = false }()

	replies := make([][]byte, len(queued))
	for i, q := range queued {
		replies[i] = q.cmd.handler(c, q.args)
	}
	return arrayReply(replies...)
}
