package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestPing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")

	// With an argument, PING replies with that argument as a bulk string.
	do(t, conn, "PING", "hello world")
	expectReply(t, conn, "$11\r\nhello world\r\n")

	do(t, conn, "PING", "too", "many")
	expectReply(t, conn, "-ERR wrong number of arguments for 'ping' command\r\n")
}

func TestEcho(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, "$3\r\nhey\r\n")
}

func TestEchoRandomArgument(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply can't be hardcoded: it has to be whatever the client sent.
	for i := 0; i < 5; i++ {
		raw := make([]byte, 8+i)
		if _, err := rand.Read(raw); err != nil {
			t.Fatalf("generating a random argument: %v", err)
		}
		message := hex.EncodeToString(raw)

		do(t, conn, "ECHO", message)
		expectReply(t, conn, fmt.Sprintf("$%d\r\n%s\r\n", len(message), message))
	}
}

func TestEchoIsBinarySafe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		name    string
		message string
		want    string
	}{
		{"empty string", "", "$0\r\n\r\n"},
		{"spaces", "hello  big world", "$16\r\nhello  big world\r\n"},
		{"embedded CRLF", "a\r\nb", "$4\r\na\r\nb\r\n"},
		{"null byte", "a\x00b", "$3\r\na\x00b\r\n"},
		{"multi-byte characters", "héllo ✓", "$10\r\nhéllo ✓\r\n"},
		{"looks like RESP", "$3\r\nfoo\r\n", "$9\r\n$3\r\nfoo\r\n\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			do(t, conn, "ECHO", tt.message)
			expectReply(t, conn, tt.want)
		})
	}
}

func TestCommandNamesAreCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"ECHO", "echo", "EcHo", "eCHO"} {
		// Only the command name is case-insensitive: the argument is untouched.
		do(t, conn, name, "MiXeD")
		expectReply(t, conn, "$5\r\nMiXeD\r\n")
	}

	for _, name := range []string{"PING", "ping", "PiNg"} {
		do(t, conn, name)
		expectReply(t, conn, "+PONG\r\n")
	}
}

func TestEchoWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ECHO")
	expectReply(t, conn, "-ERR wrong number of arguments for 'echo' command\r\n")

	do(t, conn, "echo", "a", "b")
	expectReply(t, conn, "-ERR wrong number of arguments for 'echo' command\r\n")

	// The connection stays usable after an error reply.
	do(t, conn, "ECHO", "still here")
	expectReply(t, conn, "$10\r\nstill here\r\n")
}

func TestUnknownCommand(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "NOSUCHCOMMAND")
	expectReply(t, conn, "-ERR unknown command 'NOSUCHCOMMAND', with args beginning with: \r\n")

	do(t, conn, "foo", "bar", "baz")
	expectReply(t, conn, "-ERR unknown command 'foo', with args beginning with: 'bar' 'baz' \r\n")

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestInlineCommands(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Commands typed by hand (e.g. through netcat) aren't RESP encoded.
	send(t, conn, "PING\r\n")
	expectReply(t, conn, "+PONG\r\n")

	send(t, conn, "echo hey\r\n")
	expectReply(t, conn, "$3\r\nhey\r\n")
}
