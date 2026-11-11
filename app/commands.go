package main

import (
	"strconv"
	"strings"
)

// handler runs a command and returns its RESP-encoded reply, or nil if
// nothing must be sent to the client. args holds the command's arguments,
// without the command name.
//
// Handlers are called with the server's lock held, so they can freely use the
// server's state and each command is atomic with respect to the others. A
// handler that has to wait for something releases the lock while it waits,
// and must hold it again when it returns.
type handler func(c *client, args []string) []byte

// command describes a command the server knows about.
type command struct {
	// arity is the number of arguments the command takes, including its own
	// name. A negative value means "at least -arity".
	arity   int
	handler handler
	// immediate is set for the commands that are executed right away even
	// inside a transaction, where every other command is queued instead. The
	// commands that control transactions are.
	immediate bool
	// pubsub is set for the commands a client can still send once it is
	// subscribed to channels: the ones that are about its subscriptions, and
	// PING. Every other command is refused then.
	pubsub bool
	// container is set for the commands whose first argument selects a
	// subcommand, such as CONFIG GET.
	container bool
}

// commands maps upper-case command names to their implementation.
var commands = map[string]command{
	"PING":   {arity: -1, handler: cmdPing, pubsub: true},
	"ECHO":   {arity: 2, handler: cmdEcho},
	"SET":    {arity: -3, handler: cmdSet},
	"GET":    {arity: 2, handler: cmdGet},
	"TYPE":   {arity: 2, handler: cmdType},
	"STRLEN": {arity: 2, handler: cmdStrlen},
	"INCR":   {arity: 2, handler: cmdIncr},
	"KEYS":   {arity: 2, handler: cmdKeys},
	"INFO":   {arity: -1, handler: cmdInfo},

	"CONFIG": {arity: -2, handler: cmdConfig, container: true},

	"RPUSH":  {arity: -3, handler: cmdRPush},
	"LPUSH":  {arity: -3, handler: cmdLPush},
	"LRANGE": {arity: 4, handler: cmdLRange},
	"LLEN":   {arity: 2, handler: cmdLLen},
	"LPOP":   {arity: -2, handler: cmdLPop},
	"BLPOP":  {arity: -3, handler: cmdBLPop},

	"XADD":   {arity: -5, handler: cmdXAdd},
	"XRANGE": {arity: 4, handler: cmdXRange},
	"XREAD":  {arity: -4, handler: cmdXRead},

	// REPLCONF is about the connection, not the dataset: like the commands
	// that control transactions it is never queued, so that a replica can
	// acknowledge at any point of the replication stream.
	"REPLCONF": {arity: -1, handler: cmdReplconf, immediate: true},
	"PSYNC":    {arity: 3, handler: cmdPsync},
	"WAIT":     {arity: 3, handler: cmdWait},

	"MULTI":   {arity: 1, handler: cmdMulti, immediate: true},
	"EXEC":    {arity: 1, handler: cmdExec, immediate: true},
	"DISCARD": {arity: 1, handler: cmdDiscard, immediate: true},
	"WATCH":   {arity: -2, handler: cmdWatch, immediate: true},
	"UNWATCH": {arity: 1, handler: cmdUnwatch},

	"ZADD":   {arity: -4, handler: cmdZAdd},
	"ZRANK":  {arity: 3, handler: cmdZRank},
	"ZRANGE": {arity: -4, handler: cmdZRange},
	"ZCARD":  {arity: 2, handler: cmdZCard},
	"ZSCORE": {arity: 3, handler: cmdZScore},
	"ZREM":   {arity: -3, handler: cmdZRem},

	"SETBIT":   {arity: 4, handler: cmdSetBit},
	"GETBIT":   {arity: 3, handler: cmdGetBit},
	"BITCOUNT": {arity: -2, handler: cmdBitCount},
	"BITOP":    {arity: -4, handler: cmdBitOp},

	"GEOADD": {arity: -5, handler: cmdGeoAdd},
	"GEOPOS": {arity: -2, handler: cmdGeoPos},

	"SUBSCRIBE":   {arity: -2, handler: cmdSubscribe, pubsub: true},
	"UNSUBSCRIBE": {arity: -1, handler: cmdUnsubscribe, pubsub: true},
	"PUBLISH":     {arity: 3, handler: cmdPublish},
}

// execute runs a single command sent by the client and returns the reply to
// send back, or nil if there is none left to send. Command names are
// case-insensitive.
func (c *client) execute(args []string) []byte {
	name := strings.ToUpper(args[0])

	cmd, ok := commands[name]
	if !ok {
		return unknownCommandError(args)
	}
	if (cmd.arity > 0 && len(args) != cmd.arity) || (cmd.arity < 0 && len(args) < -cmd.arity) {
		return wrongArgumentCountError(name)
	}

	c.server.mu.Lock()
	defer c.server.mu.Unlock()

	reply := c.run(cmd, args)
	// As soon as the lock is released, other clients can have things sent to
	// a client that has an outbox. The reply is queued before that happens,
	// so that it comes first: a client learns that it is subscribed to a
	// channel before it gets anything published to that channel.
	if reply != nil && c.outbox != nil {
		c.outbox.push(reply)
		return nil
	}
	return reply
}

// run executes a command that is known to exist and to have a valid number
// of arguments, given as the client sent it, and returns its reply. It is
// called with the server's lock held.
func (c *client) run(cmd command, args []string) []byte {
	// A client that is subscribed to channels uses its connection to receive
	// what is published to them. The only commands it can still send are the
	// ones made for that mode.
	if c.inSubscribedMode() && !cmd.pubsub {
		return errorReply("ERR Can't execute '" + cmd.fullName(args) + "': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context")
	}

	// Inside a transaction commands are only recorded: they have no effect,
	// and nothing is read, before the transaction is executed.
	if c.inTransaction && !cmd.immediate {
		c.queued = append(c.queued, queuedCommand{cmd: cmd, args: args[1:]})
		return simpleString("QUEUED")
	}
	return cmd.handler(c, args[1:])
}

// fullName returns the name Redis gives the command in its error messages,
// given the command as the client sent it: the name in lower case, followed
// by "|" and the subcommand for the commands that have subcommands.
func (cmd command) fullName(args []string) string {
	name := strings.ToLower(args[0])
	if cmd.container && len(args) > 1 {
		name += "|" + strings.ToLower(args[1])
	}
	return name
}

// unknownCommandError mirrors the error Redis sends for unknown commands.
func unknownCommandError(args []string) []byte {
	var b strings.Builder
	b.WriteString("ERR unknown command '" + args[0] + "', with args beginning with: ")
	for _, arg := range args[1:] {
		b.WriteString("'" + arg + "' ")
	}
	return errorReply(b.String())
}

// wrongArgumentCountError mirrors the error Redis sends when a command gets
// too few or too many arguments.
func wrongArgumentCountError(name string) []byte {
	return errorReply("ERR wrong number of arguments for '" + strings.ToLower(name) + "' command")
}

// errNotAnInteger is the error sent when an argument, or a stored value, that
// must be an integer isn't one.
const errNotAnInteger = "ERR value is not an integer or out of range"

// parseInt parses a signed 64-bit decimal integer as strictly as Redis does:
// only the canonical way of writing a number is accepted. Spaces, a leading
// "+", leading zeros or "-0" all make the string not an integer.
func parseInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	// strconv is more lenient than Redis; the canonical form is the one that
	// formatting the number gives back.
	if strconv.FormatInt(n, 10) != s {
		return 0, false
	}
	return n, true
}

// parseIndex parses an integer argument that is used as an index or a count,
// and so has to fit in an int.
func parseIndex(s string) (int, bool) {
	n, ok := parseInt(s)
	if !ok || int64(int(n)) != n {
		return 0, false
	}
	return int(n), true
}

// indexRange resolves the range that goes from index start to index stop,
// both included, of a sequence of n elements: it returns the positions the
// range covers as the half-open interval [from, to).
//
// Indexes start at 0. Negative indexes count from the end of the sequence: -1
// is the last element, -2 the one before, and so on. Indexes that fall
// outside of the sequence are brought back to its closest end, and a range
// that covers no element (such as one starting after its own stop) is empty:
// from and to are then equal.
func indexRange(n, start, stop int) (from, to int) {
	if start < 0 {
		start = max(start+n, 0)
	}
	if stop < 0 {
		stop += n
	}
	if stop >= n {
		stop = n - 1
	}
	if start > stop {
		return 0, 0
	}
	return start, stop + 1
}

// cmdPing implements PING [message]: it replies PONG, or echoes the message.
//
// A client in subscribed mode gets an array instead, made of "pong" and the
// message, which is empty if there is none. Everything such a client receives
// is an array that starts with the kind of thing it is, as what is published
// to its channels can arrive at any time: that's how it tells them apart.
func cmdPing(c *client, args []string) []byte {
	if len(args) > 1 {
		return wrongArgumentCountError("PING")
	}

	if c.inSubscribedMode() {
		message := ""
		if len(args) == 1 {
			message = args[0]
		}
		return bulkStringArray([]string{"pong", message})
	}
	if len(args) == 1 {
		return bulkString(args[0])
	}
	return simpleString("PONG")
}

// cmdEcho implements ECHO message: it replies with the message itself.
func cmdEcho(c *client, args []string) []byte {
	return bulkString(args[0])
}

// cmdType implements TYPE key: it replies with the name of the type of the
// value stored at key, or "none" if the key doesn't exist.
func cmdType(c *client, args []string) []byte {
	e, ok := c.server.store.lookup(args[0])
	if !ok {
		return simpleString("none")
	}
	return simpleString(e.typeName())
}
