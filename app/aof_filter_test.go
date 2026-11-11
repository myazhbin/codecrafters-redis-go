package main

import (
	"testing"
	"time"
)

func TestOnlyWriteCommandsAreAppended(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "my-aof-dir", "commands.aof", "random-name.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir",
		"--appendfilename", "commands.aof", "--appendfsync", "always")
	addr := startServer(t, srv)

	for _, command := range [][]string{
		{"SET", "foo", "1"},
		{"GET", "foo"},
		{"PING"},
		{"ECHO", "hello"},
		{"CONFIG", "GET", "appendonly"},
		{"SET", "bar", "2"},
	} {
		// One connection per command, like redis-cli does.
		conn := dial(t, addr)
		do(t, conn, command...)
		drainPendingReply(t, conn)
	}

	want := "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$1\r\n1\r\n" + "*3\r\n$3\r\nSET\r\n$3\r\nbar\r\n$1\r\n2\r\n"
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestCommandsThatDoNotModifyAreNeverAppended(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	setup := [][]string{
		{"SET", "text", "abc"},
		{"RPUSH", "list_key", "a"},
		{"XADD", "stream_key", "5-5", "k", "v"},
	}
	want := ""
	for _, command := range setup {
		do(t, conn, command...)
		want += encodeCommand(command...)
	}

	for _, command := range [][]string{
		// Reads.
		{"PING"}, {"PING", "message"}, {"ECHO", "hello"},
		{"GET", "text"}, {"GET", "missing"}, {"TYPE", "text"}, {"KEYS", "*"},
		{"LRANGE", "list_key", "0", "-1"}, {"LLEN", "list_key"},
		{"XRANGE", "stream_key", "-", "+"}, {"XREAD", "STREAMS", "stream_key", "0-0"},
		{"INFO", "replication"}, {"CONFIG", "GET", "dir"},
		// Commands about the connection.
		{"WATCH", "text"}, {"UNWATCH"}, {"MULTI"}, {"DISCARD"}, {"MULTI"}, {"EXEC"},
		{"REPLCONF", "capa", "psync2"}, {"WAIT", "0", "0"},
		// Writes that fail or change nothing.
		{"INCR", "text"}, {"INCR", "list_key"}, {"RPUSH", "text", "a"},
		{"LPOP", "missing"}, {"LPOP", "list_key", "0"}, {"LPOP", "text"},
		{"XADD", "stream_key", "1-1", "k", "v"},
		{"SET", "foo", "1", "PX", "abc"}, {"SET", "foo"},
		{"BLPOP", "missing", "0.01"},
		{"NOSUCHCOMMAND"},
	} {
		do(t, conn, command...)
	}
	time.Sleep(50 * time.Millisecond) // let BLPOP time out
	drainPendingReply(t, conn)

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	// Nothing was flushed for them either.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.aofSyncs != len(setup) {
		t.Errorf("the file was flushed %d times, want once per write: %d", srv.aofSyncs, len(setup))
	}
}

func TestReadsBetweenWritesLeaveNoTraceInTheFile(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	writes := [][]string{
		{"SET", "a", "1"},
		{"INCR", "a"},
		{"RPUSH", "l", "x"},
	}
	for _, write := range writes {
		do(t, conn, "GET", "a")
		do(t, conn, write...)
		do(t, conn, "PING")
	}
	drainPendingReply(t, conn)

	logged := parseCommands(t, readFile(t, incrPath))
	if len(logged) != len(writes) {
		t.Fatalf("append-only file holds %d commands, want %d: %q", len(logged), len(writes), logged)
	}
	for i, write := range writes {
		if encodeCommand(logged[i]...) != encodeCommand(write...) {
			t.Errorf("command %d of the file is %q, want %q", i, logged[i], write)
		}
	}
}

func TestReadOnlyTransactionIsNotAppended(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "MULTI")
	do(t, conn, "GET", "foo")
	do(t, conn, "PING")
	do(t, conn, "EXEC")
	drainPendingReply(t, conn)

	if got := readFile(t, incrPath); got != "" {
		t.Errorf("append-only file holds %q, want nothing", got)
	}

	// In a transaction that does both, only the writes are logged.
	do(t, conn, "MULTI")
	do(t, conn, "GET", "foo")
	do(t, conn, "SET", "foo", "1")
	do(t, conn, "GET", "foo")
	do(t, conn, "EXEC")
	drainPendingReply(t, conn)

	want := encodeCommand("MULTI") + encodeCommand("SET", "foo", "1") + encodeCommand("EXEC")
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}
