package main

import "testing"

// subscribedPong returns the RESP encoding of the reply to PING in subscribed
// mode: an array holding "pong" and the message sent with PING, which is
// empty when there is none.
func subscribedPong(message string) string {
	return bulkArray("pong", message)
}

func TestPingInSubscribedMode(t *testing.T) {
	addr := startTestServer(t)
	subscriber := dial(t, addr)

	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))
	do(t, subscriber, "PING")
	expectReply(t, subscriber, "*2\r\n$4\r\npong\r\n$0\r\n\r\n")

	// A client that isn't subscribed to anything gets the usual reply.
	other := dial(t, addr)
	do(t, other, "PING")
	expectReply(t, other, "+PONG\r\n")
}

func TestPingChangesItsReplyWhenTheClientSubscribes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	for range 3 {
		do(t, conn, "PING")
		expectReply(t, conn, subscribedPong(""))
	}

	// PING takes the client out of nothing: it is still subscribed.
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, subscribed("bar", 2))
	do(t, conn, "PING")
	expectReply(t, conn, subscribedPong(""))
}

func TestPingWithAMessageInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	// The message takes the place of the empty string.
	do(t, conn, "PING", "hello")
	expectReply(t, conn, "*2\r\n$4\r\npong\r\n$5\r\nhello\r\n")

	for _, message := range []string{"", "hello  big world", "a\r\nb", "a\x00b", "héllo ✓", "pong"} {
		do(t, conn, "PING", message)
		expectReply(t, conn, subscribedPong(message))
	}

	random := randomWord(t)
	do(t, conn, "PING", random)
	expectReply(t, conn, subscribedPong(random))
}

func TestPingCommandNameIsCaseInsensitiveInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	for _, name := range []string{"PING", "ping", "PiNg"} {
		do(t, conn, name)
		// The reply is in lower case whatever the client sent.
		expectReply(t, conn, subscribedPong(""))
	}
}

func TestPingWrongNumberOfArgumentsInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	do(t, conn, "PING", "too", "many")
	expectReply(t, conn, "-ERR wrong number of arguments for 'ping' command\r\n")
	do(t, conn, "PING")
	expectReply(t, conn, subscribedPong(""))
}

func TestPingReplyIsPerClient(t *testing.T) {
	addr := startTestServer(t)
	first, second := dial(t, addr), dial(t, addr)

	do(t, first, "SUBSCRIBE", "foo")
	expectReply(t, first, subscribed("foo", 1))

	// Only the client that subscribed is in subscribed mode, including with
	// regard to the clients that connect later.
	third := dial(t, addr)
	for range 2 {
		do(t, second, "PING")
		expectReply(t, second, "+PONG\r\n")
		do(t, first, "PING")
		expectReply(t, first, subscribedPong(""))
		do(t, third, "PING", "hey")
		expectReply(t, third, bulk("hey"))
	}

	// Subscribing changes the reply for that client, and for it alone.
	do(t, second, "SUBSCRIBE", "foo")
	expectReply(t, second, subscribed("foo", 1))
	do(t, second, "PING")
	expectReply(t, second, subscribedPong(""))
	do(t, third, "PING")
	expectReply(t, third, "+PONG\r\n")
}

func TestPingAfterASubscriberReconnects(t *testing.T) {
	addr := startTestServer(t)

	conn := dial(t, addr)
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	do(t, conn, "PING")
	expectReply(t, conn, subscribedPong(""))
	conn.Close()

	// The mode belongs to the connection: a new one starts out of it.
	conn = dial(t, addr)
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestPipelinedPingsAroundASubscription(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The commands arrive in a single write: each PING is answered according
	// to the mode the client is in when its turn comes.
	send(t, conn, encodeCommand("PING")+encodeCommand("PING", "before")+encodeCommand("SUBSCRIBE", "foo")+
		encodeCommand("PING")+encodeCommand("PING", "after"))
	expectReply(t, conn, "+PONG\r\n"+bulk("before")+subscribed("foo", 1)+
		subscribedPong("")+subscribedPong("after"))
}

func TestPingInATransactionThatSubscribes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{{"PING"}, {"SUBSCRIBE", "foo"}, {"PING"}, {"PING", "hey"}} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// The client enters subscribed mode halfway through the transaction.
	do(t, conn, "EXEC")
	expectReply(t, conn, array("+PONG\r\n", subscribed("foo", 1), subscribedPong(""), subscribedPong("hey")))
}
