package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This file implements replication: how a master keeps its replicas up to
// date, and how a replica follows its master.

// replicationIDLength is the number of characters of a replication ID.
const replicationIDLength = 40

// newReplicationID returns a new pseudo-random replication ID, made of 40
// hexadecimal digits.
func newReplicationID() string {
	b := make([]byte, replicationIDLength/2)
	// rand.Read never fails: it panics if the system can't provide
	// randomness.
	rand.Read(b)
	return hex.EncodeToString(b)
}

// masterDialTimeout is how long a replica waits for the connection to its
// master to be established.
const masterDialTimeout = 5 * time.Second

// followMaster makes a replica connect to its master, introduce itself with
// the replication handshake, receive the master's snapshot and then apply
// the commands the master propagates, for as long as the connection lasts. It
// only returns once the connection is over, with the error that ended it if
// there is one.
func (s *Server) followMaster() error {
	master := s.cfg.replicaOf.String()

	conn, err := net.DialTimeout("tcp", master, masterDialTimeout)
	if err != nil {
		return fmt.Errorf("connecting to the master at %s: %w", master, err)
	}
	defer conn.Close()

	// Bytes are counted as they come in, to know how far into the replication
	// stream the replica is.
	received := &countingReader{r: conn}
	r := bufio.NewReader(received)
	if err := s.handshake(conn, r); err != nil {
		return fmt.Errorf("handshake with the master at %s: %w", master, err)
	}

	if _, err := readRDBTransfer(r); err != nil {
		return fmt.Errorf("receiving the snapshot of the master at %s: %w", master, err)
	}

	if err := s.applyReplicationStream(conn, r, received); err != nil {
		return fmt.Errorf("reading from the master at %s: %w", master, err)
	}
	return nil
}

// readRDBTransfer reads the RDB file a master sends after FULLRESYNC, as
// "$<length>\r\n<contents>", and returns its contents. Unlike a bulk string,
// the contents aren't followed by CRLF.
func readRDBTransfer(r *bufio.Reader) ([]byte, error) {
	header, err := readLine(r)
	if err != nil {
		return nil, midCommand(err)
	}
	if !strings.HasPrefix(header, "$") {
		return nil, fmt.Errorf("%w: expected '$', got '%s'", errProtocol, firstChar(header))
	}
	length, err := strconv.Atoi(header[1:])
	if err != nil || length < 0 || length > maxBulkLength {
		return nil, fmt.Errorf("%w: invalid RDB file length", errProtocol)
	}

	file := make([]byte, length)
	if _, err := io.ReadFull(r, file); err != nil {
		return nil, midCommand(err)
	}
	return file, nil
}

// applyReplicationStream executes the commands the master propagates over
// conn, one after another, until the connection is over. They are executed
// exactly like the commands of a client, except that the master isn't sent
// any reply: it doesn't expect one.
//
// The one exception is REPLCONF GETACK, which the master sends to ask how far
// the replica got: it is answered with REPLCONF ACK and the replica's offset.
//
// r reads from received, which counts the bytes that came from the master.
// The replication stream starts with the next byte r returns.
//
// It returns nil when the master closes the connection between two commands,
// and the error that made reading fail otherwise.
func (s *Server) applyReplicationStream(conn net.Conn, r *bufio.Reader, received *countingReader) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The master is a client like any other for the commands it runs, with
	// its own transaction state.
	master := &client{server: s, conn: conn, ctx: ctx, isMaster: true}
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		master.unwatch()
	}()

	// consumed returns the number of bytes handed out by r so far: what was
	// received, minus what r read ahead and still holds.
	consumed := func() int64 { return received.n - int64(r.Buffered()) }
	start := consumed()

	s.mu.Lock()
	s.masterOffset = 0
	s.mu.Unlock()

	for {
		args, err := readCommand(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		reply := master.execute(args)
		if isGetAck(args) && reply != nil {
			if _, err := conn.Write(reply); err != nil {
				return err
			}
		}

		// The command only counts as processed once it has been executed:
		// the offset sent in reply to GETACK doesn't include GETACK itself.
		s.mu.Lock()
		s.masterOffset = consumed() - start
		s.mu.Unlock()
	}
}

// isGetAck reports whether the command is REPLCONF GETACK.
func isGetAck(args []string) bool {
	return len(args) >= 2 && strings.EqualFold(args[0], "REPLCONF") && strings.EqualFold(args[1], "GETACK")
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// handshake is how a replica introduces itself to its master, over the
// connection it just opened: it sends commands one at a time, waiting for the
// master to answer each of them before sending the next.
//
//   - PING checks that the master is there and answers.
//   - REPLCONF listening-port tells the master which port the replica serves
//     its own clients on.
//   - REPLCONF capa psync2 tells the master the replica understands the
//     second version of the synchronization protocol.
//   - PSYNC asks the master to bring the replica up to date. A replica that
//     has nothing yet doesn't know its master's replication ID and has no
//     offset, which it says by sending "?" and -1. The master answers with
//     FULLRESYNC, followed by its replication ID and its offset: the replica
//     is going to receive the whole dataset.
func (s *Server) handshake(w io.Writer, r *bufio.Reader) error {
	steps := []struct {
		command []string
		// reply is the status the master is expected to answer with.
		reply string
	}{
		{[]string{"PING"}, "PONG"},
		{[]string{"REPLCONF", "listening-port", strconv.Itoa(s.cfg.port)}, "OK"},
		{[]string{"REPLCONF", "capa", "psync2"}, "OK"},
	}

	for _, step := range steps {
		reply, err := roundTrip(w, r, step.command...)
		if err != nil {
			return fmt.Errorf("%s: %w", strings.Join(step.command, " "), err)
		}
		if reply != "+"+step.reply {
			return fmt.Errorf("%s: unexpected reply %q from the master, want %q",
				strings.Join(step.command, " "), reply, "+"+step.reply)
		}
	}

	reply, err := roundTrip(w, r, "PSYNC", "?", "-1")
	if err != nil {
		return fmt.Errorf("PSYNC: %w", err)
	}
	if _, _, err := parseFullResync(reply); err != nil {
		return fmt.Errorf("PSYNC: %w", err)
	}
	return nil
}

// parseFullResync parses the master's reply to PSYNC when it announces a full
// synchronization, "+FULLRESYNC <replication ID> <offset>", and returns the
// master's replication ID and offset.
func parseFullResync(reply string) (replID string, offset int64, err error) {
	invalid := fmt.Errorf("unexpected reply %q from the master, want \"+FULLRESYNC <replication ID> <offset>\"", reply)

	parts := strings.Fields(reply)
	if len(parts) != 3 || parts[0] != "+FULLRESYNC" {
		return "", 0, invalid
	}
	offset, ok := parseInt(parts[2])
	if !ok || offset < 0 {
		return "", 0, invalid
	}
	return parts[1], offset, nil
}

// roundTrip sends a command and waits for its reply, which must fit on a
// single line: a simple string, an error or an integer. It returns that line,
// type prefix included and line terminator excluded.
func roundTrip(w io.Writer, r *bufio.Reader, command ...string) (string, error) {
	if _, err := w.Write(encodeCommandArray(command...)); err != nil {
		return "", err
	}
	reply, err := readLine(r)
	if err != nil {
		return "", fmt.Errorf("waiting for the reply: %w", midCommand(err))
	}
	return reply, nil
}

// encodeCommandArray encodes a command the way clients send it: as a RESP
// array of bulk strings holding the command's name and its arguments.
func encodeCommandArray(args ...string) []byte {
	return bulkStringArray(args)
}

// cmdReplconf implements REPLCONF option value [option value ...], which a
// replica sends during the handshake to tell its master about itself:
//
//   - "listening-port <port>" is the port the replica serves its clients on.
//   - "capa <capability>" names a feature of the replication protocol the
//     replica supports. Capabilities the master doesn't know are ignored.
//
// Masters use it too, once the handshake is over: "GETACK *" asks the replica
// how much of the replication stream it has processed. The replica answers
// with the command "REPLCONF ACK <offset>" rather than with a regular reply.
// GETACK only means something coming from the master: from anyone else it is
// ignored, without a reply.
//
// Option names are case-insensitive.
func cmdReplconf(c *client, args []string) []byte {
	if len(args)%2 != 0 {
		return errorReply("ERR syntax error")
	}

	if strings.EqualFold(args[0], "GETACK") {
		if c.executing {
			return errorReply("ERR REPLCONF GETACK is not allowed in a transaction")
		}
		if !c.isMaster {
			return nil
		}
		return encodeCommandArray("REPLCONF", "ACK", strconv.FormatInt(c.server.masterOffset, 10))
	}

	// Nothing is applied before every option is known to be valid.
	listeningPort := c.listeningPort
	for i := 0; i < len(args); i += 2 {
		option, value := args[i], args[i+1]
		switch strings.ToLower(option) {
		case "listening-port":
			port, ok := parseInt(value)
			if !ok || port < 0 || port > 65535 {
				return errorReply(errNotAnInteger)
			}
			listeningPort = int(port)
		case "capa":
		default:
			return errorReply("ERR Unrecognized REPLCONF option: " + option)
		}
	}
	c.listeningPort = listeningPort
	return simpleString("OK")
}

// cmdPsync implements PSYNC replication-id offset, the last step of the
// handshake: the replica asks to be brought up to date, saying which history
// of the dataset it knows (a master's replication ID) and how far into it it
// got (an offset). A replica that has nothing sends "?" and -1.
//
// The master always answers with a full synchronization, whatever the replica
// already has: "+FULLRESYNC <replication ID> <offset>" tells the replica the
// master's replication ID and the offset the replica starts from. It is
// followed by a snapshot of the dataset, as an RDB file, that the replica
// loads in place of what it has.
func cmdPsync(c *client, args []string) []byte {
	if _, ok := parseInt(args[1]); !ok {
		return errorReply(errNotAnInteger)
	}
	if c.executing {
		return errorReply("ERR PSYNC is not allowed in a transaction")
	}
	s := c.server

	reply := simpleString("FULLRESYNC " + s.replID + " " + strconv.FormatInt(s.replOffset, 10))
	reply = append(reply, rdbTransfer(emptyRDB())...)

	// The snapshot is queued in the same breath as the client becomes a
	// replica, with the server's lock held: the commands propagated from now
	// on can only come after it.
	s.addReplica(c)
	c.outbox.push(reply)
	return nil
}

// addReplica makes the client a replica of the server: it receives the
// commands that modify the dataset from now on. Doing it again for a client
// that is already a replica changes nothing.
func (s *Server) addReplica(c *client) {
	if c.outbox == nil {
		c.outbox = newOutbox()
		go c.outbox.run(c.conn)
	}
	s.replicas[c] = struct{}{}
}

// removeReplica makes the server forget about a replica that is gone. It does
// nothing if the client isn't a replica.
func (s *Server) removeReplica(c *client) {
	delete(s.replicas, c)
	if c.outbox != nil {
		c.outbox.close()
	}
}

// propagate hands a command that modified the dataset over to the replicas,
// which keep their copy of the dataset up to date by executing the same
// commands as the master, in the same order. The command is given as it must
// be executed by the replicas, name first.
//
// It must be called by every command that changes the dataset, once the
// change is made and with the server's lock still held: that's what keeps
// the replication stream in the order the changes happened.
func (s *Server) propagate(command ...string) {
	if s.batching {
		s.batch = append(s.batch, command)
		return
	}
	s.feedReplicas(encodeCommandArray(command...))
}

// beginPropagationBatch makes the commands propagated from now on be held
// back until endPropagationBatch is called.
func (s *Server) beginPropagationBatch() {
	s.batching = true
}

// endPropagationBatch sends the replicas the commands that were held back
// since beginPropagationBatch, wrapped in MULTI and EXEC so that the replicas
// execute them as a single transaction. Nothing is sent if no command was
// propagated in the meantime.
func (s *Server) endPropagationBatch() {
	batch := s.batch
	s.batching, s.batch = false, nil
	if len(batch) == 0 {
		return
	}

	stream := encodeCommandArray("MULTI")
	for _, command := range batch {
		stream = append(stream, encodeCommandArray(command...)...)
	}
	stream = append(stream, encodeCommandArray("EXEC")...)
	s.feedReplicas(stream)
}

// feedReplicas appends data to the replication stream: it is sent to every
// replica and the replication offset moves forward by its length. As long as
// the server has no replica there is no stream, and nothing to do.
func (s *Server) feedReplicas(data []byte) {
	if len(s.replicas) == 0 {
		return
	}
	s.replOffset += int64(len(data))
	for replica := range s.replicas {
		replica.outbox.push(data)
	}
}

// outbox is a queue of data waiting to be written to a connection. Pushing to
// it never blocks, however slow the other end is at reading: the server can
// feed its replicas while holding its lock without ever being held up by one
// of them.
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

// rdbTransfer encodes an RDB file the way a master sends it to a replica:
// "$<length>\r\n<contents>". It looks like a bulk string, but there is no
// CRLF after the contents.
func rdbTransfer(file []byte) []byte {
	b := make([]byte, 0, len(file)+16)
	b = append(b, '$')
	b = strconv.AppendInt(b, int64(len(file)), 10)
	b = append(b, '\r', '\n')
	return append(b, file...)
}
