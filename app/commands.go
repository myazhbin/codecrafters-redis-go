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
}

// commands maps upper-case command names to their implementation.
var commands = map[string]command{
	"PING": {arity: -1, handler: cmdPing},
	"ECHO": {arity: 2, handler: cmdEcho},
	"SET":  {arity: -3, handler: cmdSet},
	"GET":  {arity: 2, handler: cmdGet},
	"TYPE": {arity: 2, handler: cmdType},
	"INCR": {arity: 2, handler: cmdIncr},

	"RPUSH":  {arity: -3, handler: cmdRPush},
	"LPUSH":  {arity: -3, handler: cmdLPush},
	"LRANGE": {arity: 4, handler: cmdLRange},
	"LLEN":   {arity: 2, handler: cmdLLen},
	"LPOP":   {arity: -2, handler: cmdLPop},
	"BLPOP":  {arity: -3, handler: cmdBLPop},

	"XADD":   {arity: -5, handler: cmdXAdd},
	"XRANGE": {arity: 4, handler: cmdXRange},
	"XREAD":  {arity: -4, handler: cmdXRead},

	"MULTI": {arity: 1, handler: cmdMulti},
}

// execute runs a single command sent by the client and returns the reply to
// send back, or nil if there is none. Command names are case-insensitive.
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
	return cmd.handler(c, args[1:])
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

// cmdPing implements PING [message]: it replies PONG, or echoes the message.
func cmdPing(c *client, args []string) []byte {
	switch len(args) {
	case 0:
		return simpleString("PONG")
	case 1:
		return bulkString(args[0])
	default:
		return wrongArgumentCountError("PING")
	}
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
