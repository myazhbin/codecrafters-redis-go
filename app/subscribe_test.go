package main

import (
	"strconv"
	"testing"
	"time"
)

// subscribed returns the RESP encoding of what a client receives when it has
// subscribed to a channel: an array holding "subscribe", the channel and the
// number of channels the client is subscribed to.
func subscribed(channel string, count int) string {
	return array(bulk("subscribe"), bulk(channel), ":"+strconv.Itoa(count)+"\r\n")
}

func TestSubscribe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nfoo\r\n:1\r\n")
}

func TestSubscribeToARandomChannel(t *testing.T) {
	addr := startTestServer(t)

	// The reply can't be hardcoded: it names the channel the client asked for.
	for range 3 {
		conn := dial(t, addr)
		channel := randomWord(t)
		do(t, conn, "SUBSCRIBE", channel)
		expectReply(t, conn, subscribed(channel, 1))
	}
}

func TestSubscribeCommandNameIsCaseInsensitive(t *testing.T) {
	addr := startTestServer(t)

	for _, name := range []string{"SUBSCRIBE", "subscribe", "SubScribe"} {
		conn := dial(t, addr)
		// The channel is a different matter: its name is used as it is.
		do(t, conn, name, "MyChan")
		expectReply(t, conn, subscribed("MyChan", 1))
	}
}

func TestChannelNamesAreBinarySafe(t *testing.T) {
	addr := startTestServer(t)

	tests := []struct {
		name    string
		channel string
	}{
		{"empty string", ""},
		{"spaces", "my  channel"},
		{"embedded CRLF", "a\r\nb"},
		{"null byte", "a\x00b"},
		{"multi-byte characters", "chaîne ✓"},
		{"glob characters", "news.*[a-z]?"},
		{"looks like RESP", "*1\r\n$4\r\nPING\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dial(t, addr)
			do(t, conn, "SUBSCRIBE", tt.channel)
			expectReply(t, conn, subscribed(tt.channel, 1))
		})
	}
}

func TestSubscribeWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE")
	expectReply(t, conn, "-ERR wrong number of arguments for 'subscribe' command\r\n")

	// The client isn't subscribed to anything, and can carry on.
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
}

func TestEachClientHasItsOwnSubscriptions(t *testing.T) {
	addr := startTestServer(t)
	first, second := dial(t, addr), dial(t, addr)

	do(t, first, "SUBSCRIBE", "foo")
	expectReply(t, first, subscribed("foo", 1))

	// The count is the client's own, not the number of subscriptions the
	// server knows about, nor the number of subscribers of the channel.
	do(t, second, "SUBSCRIBE", "bar")
	expectReply(t, second, subscribed("bar", 1))

	third := dial(t, addr)
	do(t, third, "SUBSCRIBE", "foo")
	expectReply(t, third, subscribed("foo", 1))
}

func TestChannelsAreNotKeys(t *testing.T) {
	addr := startTestServer(t)
	subscriber, other := dial(t, addr), dial(t, addr)

	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))

	// Subscribing to a channel creates nothing in the keyspace.
	do(t, other, "TYPE", "foo")
	expectReply(t, other, "+none\r\n")
	do(t, other, "KEYS", "*")
	expectReply(t, other, "*0\r\n")

	// And a key can have the name of a channel, whatever its type.
	do(t, other, "RPUSH", "bar", "a")
	expectReply(t, other, ":1\r\n")
	subscriber = dial(t, addr)
	do(t, subscriber, "SUBSCRIBE", "bar")
	expectReply(t, subscriber, subscribed("bar", 1))
	do(t, other, "LRANGE", "bar", "0", "-1")
	expectReply(t, other, bulkArray("a"))
}

func TestSubscribeInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, "+QUEUED\r\n")
	// The client subscribes when the transaction is executed.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(subscribed("foo", 1)))
}

func TestSubscribeIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)

	subscriber := dial(t, addr)
	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))

	writer := dial(t, addr)
	do(t, writer, "SET", "foo", "1")
	expectReply(t, writer, "+OK\r\n")

	// The write is all there is in the log and in the replication stream.
	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "1"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectPropagated(t, replica, "SET", "foo", "1")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestSubscribeInTheAppendOnlyFileSubscribesNobody(t *testing.T) {
	// SUBSCRIBE is never logged. A file that holds one anyway is replayed
	// without anyone being subscribed: there is no client to send messages to.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "before", "1"},
		[]string{"SUBSCRIBE", "foo"},
		[]string{"SET", "after", "2"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "before")
	expectReply(t, conn, bulk("1"))
	do(t, conn, "GET", "after")
	expectReply(t, conn, bulk("2"))
}

func TestSubscribeFromTheMasterSubscribesNobody(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	// A master never propagates SUBSCRIBE. If one comes anyway, the replica
	// carries on with the stream and sends nothing back.
	link.propagate([]string{"SUBSCRIBE", "foo"}, []string{"SET", "foo", "1"})

	conn := dial(t, addr)
	expectEventually(t, conn, "$1\r\n1\r\n", "GET", "foo")
	link.expectSilence(50 * time.Millisecond)
}
