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

// inSubscribedMode reports whether the client is in subscribed mode, which it
// enters by subscribing to a channel and stays in for as long as it is
// subscribed to at least one.
func (c *client) inSubscribedMode() bool {
	return len(c.subscriptions) > 0
}

// subscribe subscribes the client to channel. It does nothing if the client
// is subscribed to it already.
func (c *client) subscribe(channel string) {
	if _, ok := c.subscriptions[channel]; ok {
		return
	}
	if c.subscriptions == nil {
		c.subscriptions = make(map[string]struct{})
	}
	c.subscriptions[channel] = struct{}{}

	subscribers := c.server.subscribers[channel]
	if subscribers == nil {
		subscribers = make(map[*client]struct{})
		c.server.subscribers[channel] = subscribers
	}
	subscribers[c] = struct{}{}
}

// unsubscribe ends the client's subscription to channel. It does nothing if
// the client isn't subscribed to it.
func (c *client) unsubscribe(channel string) {
	if _, ok := c.subscriptions[channel]; !ok {
		return
	}
	delete(c.subscriptions, channel)

	subscribers := c.server.subscribers[channel]
	delete(subscribers, c)
	if len(subscribers) == 0 {
		delete(c.server.subscribers, channel)
	}
}

// unsubscribeAll ends all of the client's subscriptions.
func (c *client) unsubscribeAll() {
	for channel := range c.subscriptions {
		c.unsubscribe(channel)
	}
}

// cmdPublish implements PUBLISH channel message: it replies with the number
// of clients that are subscribed to the channel, which is 0 for a channel
// nobody listens to. The message can be any string.
//
// Subscriptions are per server: the clients counted are the ones subscribed
// through this server, and nothing is passed on to its replicas.
func cmdPublish(c *client, args []string) []byte {
	return integerReply(len(c.server.subscribers[args[0]]))
}
