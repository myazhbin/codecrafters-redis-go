package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// restartWithAOF stops srv's AOF persistence, the way a server that shuts
// down does, and returns a new server started with the same arguments: one
// that finds the files the first one left.
func restartWithAOF(t *testing.T, srv *Server, args ...string) *Server {
	t.Helper()

	if err := srv.closeAOF(); err != nil {
		t.Fatalf("closeAOF: %v", err)
	}
	return newServerWithAOF(t, args...)
}

func TestEveryCommandOfTheAppendOnlyFileIsReplayed(t *testing.T) {
	dir, _ := prepareAOF(t, "my-aof-dir", "commands.aof", "random-name.1.incr.aof",
		[]string{"SET", "foo", "100"},
		[]string{"SET", "bar", "200"},
		[]string{"SET", "baz", "300"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir", "--appendfilename", "commands.aof")
	addr := startServer(t, srv)

	// Not just the first command: all of them. One connection per GET, like
	// redis-cli does.
	for key, want := range map[string]string{"foo": "100", "bar": "200", "baz": "300"} {
		conn := dial(t, addr)
		do(t, conn, "GET", key)
		expectReply(t, conn, bulk(want))
	}

	conn := dial(t, addr)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("bar", "baz", "foo"))
}

func TestReplayAppliesTheCommandsInOrder(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "foo", "first"},
		[]string{"SET", "counter", "10"},
		[]string{"INCR", "counter"},
		[]string{"SET", "foo", "second"},
		[]string{"INCR", "counter"},
		[]string{"INCR", "counter"},
		[]string{"SET", "foo", "last"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	// The dataset is the cumulative effect of every write of the file.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, bulk("last"))
	do(t, conn, "GET", "counter")
	expectReply(t, conn, bulk("13"))
}

func TestReplayOfEveryKindOfWrite(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "text", "abc"},
		[]string{"RPUSH", "list_key", "a", "b", "c"},
		[]string{"LPUSH", "list_key", "z"},
		[]string{"LPOP", "list_key"},
		[]string{"RPUSH", "gone", "x", "y"},
		[]string{"LPOP", "gone", "2"},
		[]string{"XADD", "stream_key", "1-1", "k1", "v1"},
		[]string{"XADD", "stream_key", "1-2", "k2", "v2"},
		[]string{"INCR", "counter"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "text")
	expectReply(t, conn, bulk("abc"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("a", "b", "c"))
	do(t, conn, "XRANGE", "stream_key", "-", "+")
	expectReply(t, conn, array(entryReply("1-1", "k1", "v1"), entryReply("1-2", "k2", "v2")))
	do(t, conn, "GET", "counter")
	expectReply(t, conn, bulk("1"))

	// A list that was emptied is gone, as it was when the file was written.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("counter", "list_key", "stream_key", "text"))
}

func TestReplayIgnoresTheLetterCaseOfCommandNames(t *testing.T) {
	// Commands are logged as they were received, whatever their case.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"set", "foo", "100"},
		[]string{"Set", "bar", "200"},
		[]string{"incr", "foo"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, bulk("101"))
	do(t, conn, "GET", "bar")
	expectReply(t, conn, bulk("200"))
}

func TestReplayCarriesOnAfterACommandThatFails(t *testing.T) {
	// A command that failed when it was received isn't logged, but a file
	// that holds one anyway is replayed like the server ran it: the error
	// stops nothing.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "text", "abc"},
		[]string{"INCR", "text"},
		[]string{"NOSUCHCOMMAND", "x"},
		[]string{"SET", "after", "still replayed"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "text")
	expectReply(t, conn, bulk("abc"))
	do(t, conn, "GET", "after")
	expectReply(t, conn, bulk("still replayed"))
}

func TestReplayOfALongFile(t *testing.T) {
	// More commands than fit in what is read from the file at once.
	const count = 2000
	commands := make([][]string, 0, count+1)
	for i := range count {
		commands = append(commands, []string{"SET", "key" + strconv.Itoa(i), "value" + strconv.Itoa(i)})
	}
	commands = append(commands, []string{"SET", "key0", "overwritten at the end"})

	dir, incrPath := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof", commands...)
	before := readFile(t, incrPath)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	for _, i := range []int{1, 2, 999, 1000, count - 1} {
		do(t, conn, "GET", "key"+strconv.Itoa(i))
		expectReply(t, conn, bulk("value"+strconv.Itoa(i)))
	}
	do(t, conn, "GET", "key0")
	expectReply(t, conn, bulk("overwritten at the end"))

	srv.mu.Lock()
	keys := len(srv.store.entries)
	srv.mu.Unlock()
	if keys != count {
		t.Errorf("%d keys after the replay, want %d", keys, count)
	}
	if got := readFile(t, incrPath); got != before {
		t.Errorf("the append-only file was modified by the replay: %d bytes, want %d", len(got), len(before))
	}
}

func TestReplayOfALongFileCutShort(t *testing.T) {
	// The cut is far into the file: what is dropped is the last command only.
	var whole strings.Builder
	for i := range 2000 {
		whole.WriteString(encodeCommand("SET", "key"+strconv.Itoa(i), "value"+strconv.Itoa(i)))
	}
	partial := encodeCommand("SET", "last", "never finished")

	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	if err := os.WriteFile(incrPath, []byte(whole.String()+partial[:len(partial)-3]), 0o644); err != nil {
		t.Fatalf("writing the append-only file: %v", err)
	}
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "key1999")
	expectReply(t, conn, bulk("value1999"))
	do(t, conn, "GET", "last")
	expectReply(t, conn, "$-1\r\n")
	if got := readFile(t, incrPath); got != whole.String() {
		t.Errorf("append-only file is %d bytes long after the replay, want it truncated to %d", len(got), whole.Len())
	}
}

func TestReplayOfATransaction(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "before", "1"},
		[]string{"MULTI"},
		[]string{"SET", "foo", "41"},
		[]string{"INCR", "foo"},
		[]string{"RPUSH", "list_key", "a"},
		[]string{"EXEC"},
		[]string{"SET", "after", "2"},
	)
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, bulk("42"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("a"))
	// What follows the transaction is executed, not queued.
	do(t, conn, "GET", "after")
	expectReply(t, conn, bulk("2"))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("after", "before", "foo", "list_key"))
}

func TestReplayDropsATransactionWithoutItsExec(t *testing.T) {
	complete := encodeCommand("SET", "foo", "100")

	tests := []struct {
		name       string
		unfinished string
	}{
		{"only MULTI", encodeCommand("MULTI")},
		{"MULTI and commands", encodeCommand("MULTI") + encodeCommand("SET", "bar", "200") + encodeCommand("INCR", "foo")},
		{"cut inside a command", encodeCommand("MULTI") + encodeCommand("SET", "bar", "200") + "*3\r\n$3\r\nSET\r\n$3\r\nba"},
		{"cut inside EXEC", encodeCommand("MULTI") + encodeCommand("SET", "bar", "200") + "*1\r\n$4\r\nEX"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
			if err := os.WriteFile(incrPath, []byte(complete+tt.unfinished), 0o644); err != nil {
				t.Fatalf("writing the append-only file: %v", err)
			}
			args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}

			srv := newServerWithAOF(t, args...)
			conn := dial(t, startServer(t, srv))

			// None of the commands of the transaction had any effect.
			do(t, conn, "GET", "foo")
			expectReply(t, conn, bulk("100"))
			do(t, conn, "GET", "bar")
			expectReply(t, conn, "$-1\r\n")
			if got := readFile(t, incrPath); got != complete {
				t.Fatalf("append-only file holds %q, want it truncated to %q", got, complete)
			}

			// The writes made from now on are neither queued nor lost: they
			// are still there after another restart.
			do(t, conn, "SET", "baz", "300")
			expectReply(t, conn, "+OK\r\n")
			if got, want := readFile(t, incrPath), complete+encodeCommand("SET", "baz", "300"); got != want {
				t.Fatalf("append-only file holds %q, want %q", got, want)
			}

			conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
			do(t, conn, "GET", "baz")
			expectReply(t, conn, bulk("300"))
			do(t, conn, "KEYS", "*")
			expectReply(t, conn, bulkArray("baz", "foo"))
		})
	}
}

func TestWhatAServerLoggedIsReplayedByTheNextOne(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appenddirname", "my-aof-dir",
		"--appendfilename", "commands.aof", "--appendfsync", "always"}

	// state returns what a client of the server at addr sees of the dataset:
	// the replies to a series of reads.
	state := func(addr string) string {
		t.Helper()

		conn := dial(t, addr)
		for _, command := range [][]string{
			{"KEYS", "*"},
			{"GET", "text"},
			{"GET", "counter"},
			{"LRANGE", "list_key", "0", "-1"},
			{"XRANGE", "stream_key", "-", "+"},
			{"GET", "tx"},
		} {
			do(t, conn, command...)
		}
		// The replies are read up to the last one, which is known.
		do(t, conn, "ECHO", "end-of-state")
		const end = "$12\r\nend-of-state\r\n"
		var replies strings.Builder
		for !strings.HasSuffix(replies.String(), end) {
			replies.WriteString(readReplyLine(t, conn) + "\r\n")
		}
		return replies.String()
	}

	first := newServerWithAOF(t, args...)
	addr := startServer(t, first)
	conn := dial(t, addr)
	for _, command := range [][]string{
		{"SET", "text", "abc"},
		{"SET", "counter", "5"},
		{"INCR", "counter"},
		{"RPUSH", "list_key", "a", "b", "c"},
		{"LPOP", "list_key"},
		{"XADD", "stream_key", "1-1", "k", "v"},
		{"GET", "text"},
		{"MULTI"},
		{"SET", "tx", "1"},
		{"INCR", "tx"},
		{"EXEC"},
	} {
		do(t, conn, command...)
	}
	drainPendingReply(t, conn)
	want := state(addr)
	if !strings.Contains(want, bulkArray("counter", "list_key", "stream_key", "text", "tx")) {
		t.Fatalf("unexpected dataset before the restart: %q", want)
	}

	// A second server finds the dataset of the first one...
	second := restartWithAOF(t, first, args...)
	addr = startServer(t, second)
	if got := state(addr); got != want {
		t.Fatalf("dataset after a restart:\n%q\nwant:\n%q", got, want)
	}

	// ...and adds to its log, so a third one finds the writes of both.
	conn = dial(t, addr)
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":7\r\n")
	do(t, conn, "RPUSH", "list_key", "d")
	expectReply(t, conn, ":3\r\n")

	third := restartWithAOF(t, second, args...)
	conn = dial(t, startServer(t, third))
	do(t, conn, "GET", "counter")
	expectReply(t, conn, bulk("7"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("b", "c", "d"))
	do(t, conn, "GET", "tx")
	expectReply(t, conn, bulk("2"))
}
