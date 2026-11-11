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
	// A subscriber is sent things it didn't ask for, by the clients that
	// publish them.
	c.openOutbox()

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

// cmdPublish implements PUBLISH channel message: it sends the message, which
// can be any string, to the clients that are subscribed to the channel, and
// replies with their number. Publishing to a channel nobody is subscribed to
// is fine: the message is lost and the reply is 0.
//
// Subscriptions are per server: the message goes to the clients subscribed
// through this server, and isn't passed on to its replicas.
func cmdPublish(c *client, args []string) []byte {
	return integerReply(c.server.publish(args[0], args[1]))
}

// publish sends message to every client subscribed to channel, as an array
// made of "message", the name of the channel and the message itself. It
// returns the number of clients the message was sent to.
//
// Nothing waits for the subscribers to read: the message is left in the
// outbox of each of them, behind what they were sent before. A subscriber
// therefore gets the messages of a channel in the order they were published,
// which is the same for all of them.
func (s *Server) publish(channel, message string) int {
	subscribers := s.subscribers[channel]
	if len(subscribers) == 0 {
		return 0
	}

	// Encoded once: the subscribers share the bytes, which nobody modifies.
	data := bulkStringArray([]string{"message", channel, message})
	for subscriber := range subscribers {
		subscriber.outbox.push(data)
	}
	return len(subscribers)
}
