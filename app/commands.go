package main

import "strings"

// handler runs a command and returns its RESP-encoded reply. args holds the
// command's arguments, without the command name.
//
// Handlers are called with the server's lock held, so they can freely use the
// server's state and each command is atomic with respect to the others.
type handler func(s *Server, args []string) []byte

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

	"RPUSH":  {arity: -3, handler: cmdRPush},
	"LPUSH":  {arity: -3, handler: cmdLPush},
	"LRANGE": {arity: 4, handler: cmdLRange},
	"LLEN":   {arity: 2, handler: cmdLLen},
	"LPOP":   {arity: -2, handler: cmdLPop},
}

// execute runs a single client command and returns the reply to send back.
// Command names are case-insensitive.
func (s *Server) execute(args []string) []byte {
	name := strings.ToUpper(args[0])

	cmd, ok := commands[name]
	if !ok {
		return unknownCommandError(args)
	}
	if (cmd.arity > 0 && len(args) != cmd.arity) || (cmd.arity < 0 && len(args) < -cmd.arity) {
		return wrongArgumentCountError(name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return cmd.handler(s, args[1:])
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

// cmdPing implements PING [message]: it replies PONG, or echoes the message.
func cmdPing(s *Server, args []string) []byte {
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
func cmdEcho(s *Server, args []string) []byte {
	return bulkString(args[0])
}
