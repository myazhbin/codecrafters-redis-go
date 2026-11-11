package main

// This file implements transactions: MULTI and the commands that go with it.

// cmdMulti implements MULTI: it starts a transaction on the client's
// connection. Transactions can't be nested.
func cmdMulti(c *client, args []string) []byte {
	if c.inTransaction {
		return errorReply("ERR MULTI calls can not be nested")
	}
	c.inTransaction = true
	return simpleString("OK")
}

// cmdExec implements EXEC: it ends the transaction started by MULTI, and
// replies with an array holding the replies of the transaction's commands.
// It is an error to call it outside of a transaction.
func cmdExec(c *client, args []string) []byte {
	if !c.inTransaction {
		return errorReply("ERR EXEC without MULTI")
	}
	c.inTransaction = false
	return arrayReply()
}
