package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// readAhead is the number of commands that can be read from a client while
// it waits for the reply to an earlier one.
const readAhead = 256

// client is the server's side of a connection with a client.
type client struct {
	server *Server
	conn   net.Conn
	// ctx is cancelled as soon as the client is known to have disconnected.
	// Commands that block must stop waiting when that happens.
	ctx context.Context

	// inTransaction is set between MULTI and the end of the transaction it
	// started. Like the rest of the client's state, it belongs to this
	// connection only: other clients have their own.
	inTransaction bool
	// queued holds the commands received since MULTI, in order. They are not
	// executed until the transaction is.
	queued []queuedCommand
	// watched is the set of keys the client asked to monitor with WATCH.
	watched map[string]struct{}
	// dirty is set when one of the watched keys has been modified since the
	// client started watching it. The client's next transaction is then
	// aborted instead of being executed.
	dirty bool
	// listeningPort is the port a replica said it serves its own clients on,
	// with REPLCONF listening-port. It is 0 for clients that didn't say.
	listeningPort int
	// outbox is set once the client is sent things besides the replies to
	// its commands: when it becomes a replica, which receives the commands
	// that modify the dataset, or a subscriber, which receives what is
	// published to its channels. From then on everything sent to it, replies
	// included, goes through the outbox, so that it all reaches the client
	// whole and in the order it was produced.
	outbox *outbox
	// isMaster is set, on a replica, for the client that stands for the
	// master: the one whose commands come from the replication stream.
	isMaster bool
	// ackOffset is, for a replica, how much of the replication stream it is
	// known to have processed: the offset it started from, or the last one
	// it acknowledged with REPLCONF ACK.
	ackOffset int64
	// executing is set while EXEC runs the queued commands. Commands that
	// would otherwise block must not do so then: a transaction runs from
	// start to finish without letting any other client in.
	executing bool
	// subscriptions is the set of channels the client is subscribed to with
	// SUBSCRIBE.
	subscriptions map[string]struct{}
	// user is the user the client is authenticated as, or will be taken for
	// if it is allowed in without authenticating.
	user *user
	// authenticated is set once the client has proved who it is, or didn't
	// have to: when it connected at a time the default user had no password,
	// or when it authenticated with AUTH since. It stays set for as long as
	// the connection lasts.
	authenticated bool
}

// queuedCommand is a command waiting in a transaction.
type queuedCommand struct {
	cmd command
	// args are the command's arguments, without the command name.
	args []string
}

// request is something read from the client's connection: either a command,
// or the error that made reading fail.
type request struct {
	args []string
	err  error
}

// handleConnection serves a client until it disconnects: it executes its
// commands one after another, sending back the reply of each of them.
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &client{server: s, conn: conn, ctx: ctx, user: s.defaultUser}
	// A connection is the default user from the start, which takes no
	// authentication as long as that user has no password.
	s.mu.Lock()
	c.authenticated = s.defaultUser.nopass
	s.mu.Unlock()
	defer func() {
		// A client that is gone must not stay registered as a watcher, as a
		// subscriber or as a replica, and nothing more is sent to it.
		s.mu.Lock()
		defer s.mu.Unlock()
		c.unwatch()
		c.unsubscribeAll()
		s.removeReplica(c)
		if c.outbox != nil {
			c.outbox.close()
		}
	}()

	for req := range c.readRequests(cancel) {
		if req.err != nil {
			if errors.Is(req.err, errProtocol) {
				c.send(errorReply("ERR " + req.err.Error()))
			}
			return
		}

		reply := c.execute(req.args)
		if reply == nil {
			continue
		}
		if err := c.send(reply); err != nil {
			return
		}
	}
}

// send sends a reply to the client. It is only called by the goroutine that
// serves the client.
func (c *client) send(reply []byte) error {
	if c.outbox != nil {
		c.outbox.push(reply)
		return nil
	}
	_, err := c.conn.Write(reply)
	return err
}

// openOutbox gives the client an outbox, unless it has one already: from then
// on other clients can have things sent to it. It must be called by the
// goroutine that serves the client, with the server's lock held.
func (c *client) openOutbox() {
	if c.outbox != nil {
		return
	}
	c.outbox = newOutbox()
	go c.outbox.run(c.conn)
}

// outbox is a queue of data waiting to be written to a connection. Pushing to
// it never blocks, however slow the other end is at reading: the server can
// feed its replicas and its subscribers while holding its lock without ever
// being held up by one of them.
type outbox struct {
	mu      sync.Mutex
	ready   *sync.Cond
	pending [][]byte
	closed  bool
}

func newOutbox() *outbox {
	o := &outbox{}
	o.ready = sync.NewCond(&o.mu)
	return o
}

// push queues data to be written. Data pushed after the outbox is closed is
// dropped. The caller must not modify data afterwards.
func (o *outbox) push(data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.pending = append(o.pending, data)
	o.ready.Signal()
}

// close makes run return, dropping whatever is still waiting to be written.
func (o *outbox) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	o.pending = nil
	o.ready.Signal()
}

// run writes what is pushed to the outbox to w, in order, until the outbox is
// closed or writing fails.
func (o *outbox) run(w io.Writer) {
	for {
		o.mu.Lock()
		for len(o.pending) == 0 && !o.closed {
			o.ready.Wait()
		}
		if o.closed {
			o.mu.Unlock()
			return
		}
		pending := o.pending
		o.pending = nil
		o.mu.Unlock()

		for _, data := range pending {
			if _, err := w.Write(data); err != nil {
				o.close()
				return
			}
		}
	}
}

// readRequests reads the client's commands on a separate goroutine and
// returns the channel they are delivered on, in the order they were sent. The
// channel is closed once the connection can't be read anymore.
//
// Reading isn't done by the goroutine that executes the commands so that a
// disconnection is noticed, and reported by calling cancel, even while that
// goroutine is busy waiting inside a blocking command.
func (c *client) readRequests(cancel context.CancelFunc) <-chan request {
	requests := make(chan request, readAhead)

	go func() {
		defer close(requests)
		defer cancel()

		reader := bufio.NewReader(c.conn)
		for {
			args, err := readCommand(reader)
			select {
			case requests <- request{args: args, err: err}:
			case <-c.ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	return requests
}
