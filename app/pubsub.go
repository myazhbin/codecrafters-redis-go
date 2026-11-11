package main

// This file implements Pub/Sub: clients subscribe to channels to receive the
// messages that other clients publish to them.

// cmdSubscribe implements SUBSCRIBE channel [channel ...]: it subscribes the
// client to the channels. A channel is just a name: it doesn't have to be
// created first, and it has nothing to do with the key of the same name.
//
// Each channel gets a reply of its own, in the order the channels are given:
// an array made of "subscribe", the name of the channel and the number of
// channels the client is subscribed to at that point. Subscribing to a
// channel again changes nothing: the channel still counts once.
func cmdSubscribe(c *client, args []string) []byte {
	if !c.canSubscribe() {
		return errorReply("ERR SUBSCRIBE isn't allowed for a DENY BLOCKING client")
	}

	var reply []byte
	for _, channel := range args {
		c.subscribe(channel)
		reply = append(reply, arrayReply(
			bulkString("subscribe"),
			bulkString(channel),
			integerReply(len(c.subscriptions)),
		)...)
	}
	return reply
}

// canSubscribe reports whether the client can subscribe to channels, which
// takes someone at the other end of a connection to read what is published.
// The commands of the append-only file and of the replication stream are
// executed on behalf of clients that aren't like that: nobody reads the
// replies to the former, and what is sent to a master must be about
// replication only.
func (c *client) canSubscribe() bool {
	return c.conn != nil && !c.isMaster
}

// subscribe subscribes the client to channel. It does nothing if the client
// is subscribed to it already.
func (c *client) subscribe(channel string) {
	if c.subscriptions == nil {
		c.subscriptions = make(map[string]struct{})
	}
	c.subscriptions[channel] = struct{}{}
}
