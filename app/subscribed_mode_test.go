package main

import (
	"sort"
	"strings"
	"testing"
	"time"
)

// notAllowedInSubscribedMode returns the RESP encoding of the error a client
// in subscribed mode gets for a command it can't send in that mode. name is
// the name of the command as Redis reports it.
func notAllowedInSubscribedMode(name string) string {
	return "-ERR Can't execute '" + name + "': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context\r\n"
}

func TestCommandsAreRejectedInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nfoo\r\n:1\r\n")

	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "-ERR Can't execute 'set': only (P|S)SUBSCRIBE / (P|S)UNSUBSCRIBE / PING / QUIT / RESET are allowed in this context\r\n")
	do(t, conn, "GET", "key")
	expectReply(t, conn, notAllowedInSubscribedMode("get"))
	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, notAllowedInSubscribedMode("echo"))

	// SUBSCRIBE is one of the commands of this mode: it works as usual.
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nbar\r\n:2\r\n")
}

func TestClientEntersSubscribedModeBySubscribing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Before it subscribes to anything, a client can send any command.
	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, bulk("hey"))

	// A SUBSCRIBE that fails subscribes to nothing.
	do(t, conn, "SUBSCRIBE")
	expectReply(t, conn, "-ERR wrong number of arguments for 'subscribe' command\r\n")
	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, bulk("hey"))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, notAllowedInSubscribedMode("echo"))
}

func TestEveryOtherCommandIsRejectedInSubscribedMode(t *testing.T) {
	addr := startTestServer(t)

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		cmd := commands[name]
		if cmd.pubsub {
			continue
		}
		t.Run(name, func(t *testing.T) {
			conn := dial(t, addr)
			do(t, conn, "SUBSCRIBE", "foo")
			expectReply(t, conn, subscribed("foo", 1))

			// The command is given as many arguments as it takes. What they
			// are doesn't matter: it is refused before they are looked at.
			args := []string{name}
			for len(args) < max(cmd.arity, -cmd.arity) {
				args = append(args, "arg")
			}
			want := strings.ToLower(name)
			if cmd.container {
				want += "|arg"
			}
			do(t, conn, args...)
			expectReply(t, conn, notAllowedInSubscribedMode(want))

			// The client is still subscribed, and its connection still works.
			do(t, conn, "SUBSCRIBE", "bar")
			expectReply(t, conn, subscribed("bar", 2))
		})
	}
}

func TestRejectedCommandIsNamedInLowerCase(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "subscribe", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	// Whatever the letter case the client used.
	for _, name := range []string{"SET", "set", "sEt"} {
		do(t, conn, name, "key", "value")
		expectReply(t, conn, notAllowedInSubscribedMode("set"))
	}

	// A command that has subcommands is named along with its subcommand.
	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, notAllowedInSubscribedMode("config|get"))
	do(t, conn, "config", "Get", "dir")
	expectReply(t, conn, notAllowedInSubscribedMode("config|get"))
}

func TestRejectedCommandsHaveNoEffect(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	subscriber, other := dial(t, addr), dial(t, addr)

	do(t, other, "SET", "counter", "1")
	expectReply(t, other, "+OK\r\n")
	expectPropagated(t, replica, "SET", "counter", "1")

	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))
	for _, command := range [][]string{
		{"SET", "key", "value"},
		{"INCR", "counter"},
		{"RPUSH", "list_key", "a"},
		{"LPOP", "list_key"},
		{"XADD", "stream_key", "1-1", "k", "v"},
	} {
		do(t, subscriber, command...)
		expectReply(t, subscriber, notAllowedInSubscribedMode(strings.ToLower(command[0])))
	}

	// Nothing was written: not to the dataset, not to the append-only file,
	// not to the replication stream.
	do(t, other, "KEYS", "*")
	expectReply(t, other, bulkArray("counter"))
	do(t, other, "GET", "counter")
	expectReply(t, other, bulk("1"))
	if got, want := readFile(t, incrPath), encodeCommand("SET", "counter", "1"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestBlockingCommandsDoNotBlockInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	// The commands would wait forever for something that doesn't come. They
	// are refused right away instead.
	do(t, conn, "BLPOP", "list_key", "0")
	expectReply(t, conn, notAllowedInSubscribedMode("blpop"))
	do(t, conn, "XREAD", "BLOCK", "0", "STREAMS", "stream_key", "$")
	expectReply(t, conn, notAllowedInSubscribedMode("xread"))
	do(t, conn, "WAIT", "1", "0")
	expectReply(t, conn, notAllowedInSubscribedMode("wait"))
}

func TestTransactionsCannotBeStartedInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	do(t, conn, "MULTI")
	expectReply(t, conn, notAllowedInSubscribedMode("multi"))
	// No transaction was started: the next command is executed, not queued.
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, subscribed("bar", 2))

	for _, command := range [][]string{{"EXEC"}, {"DISCARD"}, {"WATCH", "key"}, {"UNWATCH"}} {
		do(t, conn, command...)
		expectReply(t, conn, notAllowedInSubscribedMode(strings.ToLower(command[0])))
	}
}

func TestSubscribedModeStartsWhenTheTransactionIsExecuted(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, "+QUEUED\r\n")
	// Queueing SUBSCRIBE subscribes to nothing yet: commands are still
	// accepted, and the transaction is executed whole.
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(subscribed("foo", 1), "+OK\r\n"))

	do(t, conn, "GET", "key")
	expectReply(t, conn, notAllowedInSubscribedMode("get"))
	do(t, other, "GET", "key")
	expectReply(t, other, bulk("value"))
}

func TestInvalidCommandsInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	// A command has to exist, and to be well-formed, before the mode the
	// client is in matters.
	do(t, conn, "NOSUCHCOMMAND", "arg")
	expectReply(t, conn, "-ERR unknown command 'NOSUCHCOMMAND', with args beginning with: 'arg' \r\n")
	do(t, conn, "GET")
	expectReply(t, conn, "-ERR wrong number of arguments for 'get' command\r\n")
	do(t, conn, "SUBSCRIBE")
	expectReply(t, conn, "-ERR wrong number of arguments for 'subscribe' command\r\n")

	// None of that changed the client's subscriptions.
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, subscribed("bar", 2))
}

func TestSubscribedModeIsPerClient(t *testing.T) {
	addr := startTestServer(t)
	subscriber, other := dial(t, addr), dial(t, addr)

	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))

	// The other clients aren't concerned, whether they connected before or
	// after.
	late := dial(t, addr)
	do(t, other, "SET", "key", "value")
	expectReply(t, other, "+OK\r\n")
	do(t, late, "GET", "key")
	expectReply(t, late, bulk("value"))
	do(t, other, "ECHO", "hey")
	expectReply(t, other, bulk("hey"))

	do(t, subscriber, "GET", "key")
	expectReply(t, subscriber, notAllowedInSubscribedMode("get"))
}

func TestPipelinedCommandsInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The commands arrive in a single write: each of them is still judged by
	// the mode the client is in when its turn comes.
	send(t, conn, encodeCommand("ECHO", "before")+encodeCommand("SUBSCRIBE", "foo")+
		encodeCommand("ECHO", "after")+encodeCommand("SUBSCRIBE", "bar"))
	expectReply(t, conn, bulk("before")+subscribed("foo", 1)+
		notAllowedInSubscribedMode("echo")+subscribed("bar", 2))
}

func TestPingIsAllowedInSubscribedMode(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))

	// What PING replies in this mode is another matter. It isn't refused.
	do(t, conn, "PING")
	if line := readReplyLine(t, conn); strings.HasPrefix(line, "-") {
		t.Errorf("PING was answered with the error %q in subscribed mode", line)
	}
}
