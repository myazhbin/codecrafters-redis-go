package main

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestWriteCommandsAreAppendedInOrder(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "my-aof-dir", "commands.aof", "random-name.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir",
		"--appendfilename", "commands.aof", "--appendfsync", "always")
	addr := startServer(t, srv)

	// One connection per command, like redis-cli does.
	conn := dial(t, addr)
	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	conn = dial(t, addr)
	do(t, conn, "SET", "bar", "200")
	expectReply(t, conn, "+OK\r\n")

	// One command right after the other, with nothing in between.
	want := "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\n100\r\n" + "*3\r\n$3\r\nSET\r\n$3\r\nbar\r\n$3\r\n200\r\n"
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestEachWriteIsFlushedWithTheAlwaysPolicy(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	want := ""
	for i := 1; i <= 5; i++ {
		key, value := "key"+strconv.Itoa(i), strconv.Itoa(i*100)
		do(t, conn, "SET", key, value)
		expectReply(t, conn, "+OK\r\n")
		want += encodeCommand("SET", key, value)

		// After each reply the file holds everything so far, flushed.
		if got := readFile(t, incrPath); got != want {
			t.Fatalf("after %d writes the append-only file holds %q, want %q", i, got, want)
		}
		srv.mu.Lock()
		syncs := srv.aofSyncs
		srv.mu.Unlock()
		if syncs != i {
			t.Fatalf("after %d writes the file was flushed %d times", i, syncs)
		}
	}
}

func TestEveryKindOfWriteIsAppended(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	commands := [][]string{
		{"SET", "foo", "100"},
		{"INCR", "foo"},
		{"SET", "ttl", "v", "PX", "100000"},
		{"RPUSH", "list_key", "a", "b", "c"},
		{"LPUSH", "list_key", "z"},
		{"LPOP", "list_key"},
		{"LPOP", "list_key", "2"},
		{"XADD", "stream_key", "1-1", "k", "v"},
	}
	want := ""
	for _, command := range commands {
		do(t, conn, command...)
		drainPendingReply(t, conn)
		want += encodeCommand(command...)
	}

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestTransactionIsAppendedAsATransaction(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "MULTI")
	do(t, conn, "SET", "foo", "1")
	do(t, conn, "INCR", "foo")
	drainPendingReply(t, conn)
	// Nothing is logged while the commands are only queued.
	if got := readFile(t, incrPath); got != "" {
		t.Fatalf("append-only file holds %q before EXEC, want nothing", got)
	}

	do(t, conn, "EXEC")
	expectReply(t, conn, array("+OK\r\n", ":2\r\n"))

	want := encodeCommand("MULTI") + encodeCommand("SET", "foo", "1") + encodeCommand("INCR", "foo") + encodeCommand("EXEC")
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	// The whole transaction is flushed at once.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.aofSyncs != 1 {
		t.Errorf("the file was flushed %d times for one transaction, want 1", srv.aofSyncs)
	}
}

func TestConcurrentWritesAreAllAppendedWhole(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "no")
	addr := startServer(t, srv)

	const writers, writes = 5, 20
	var wg sync.WaitGroup
	for w := range writers {
		conn := dial(t, addr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range writes {
				do(t, conn, "SET", "key"+strconv.Itoa(w), strconv.Itoa(i))
			}
			buf := make([]byte, len("+OK\r\n")*writes)
			conn.SetReadDeadline(time.Now().Add(replyTimeout))
			for n := 0; n < len(buf); {
				m, err := conn.Read(buf[n:])
				if err != nil {
					t.Errorf("reading replies: %v", err)
					return
				}
				n += m
			}
		}()
	}
	wg.Wait()

	// The file is a valid sequence of commands: none was cut by another.
	// Replaying it gives each key the last value its writer set.
	last := make(map[string]string)
	count := 0
	for _, command := range parseCommands(t, readFile(t, incrPath)) {
		if len(command) != 3 || command[0] != "SET" {
			t.Fatalf("unexpected command in the append-only file: %q", command)
		}
		last[command[1]] = command[2]
		count++
	}
	if count != writers*writes {
		t.Errorf("append-only file holds %d commands, want %d", count, writers*writes)
	}
	for w := range writers {
		if got := last["key"+strconv.Itoa(w)]; got != strconv.Itoa(writes-1) {
			t.Errorf("last value logged for key%d is %q, want %d", w, got, writes-1)
		}
	}
}

func TestEverysecPolicyFlushesPeriodically(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	cfg, err := parseConfig([]string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "everysec"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	srv := NewServerWithConfig(cfg)
	srv.aofSyncInterval = 10 * time.Millisecond // a second is long for a test
	if err := srv.setupAOF(); err != nil {
		t.Fatalf("setupAOF: %v", err)
	}
	t.Cleanup(func() { srv.closeAOF() })
	conn := dial(t, startServer(t, srv))

	syncs := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.aofSyncs
	}

	// Nothing to flush as long as nothing was written.
	time.Sleep(50 * time.Millisecond)
	if got := syncs(); got != 0 {
		t.Fatalf("the file was flushed %d times although nothing was written", got)
	}

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "100"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	eventually(t, "the append-only file to be flushed", func() bool { return syncs() == 1 })

	// One flush covers all the writes of an interval, and there's no flush
	// without new writes.
	time.Sleep(50 * time.Millisecond)
	if got := syncs(); got != 1 {
		t.Errorf("the file was flushed %d times for one write, want 1", got)
	}
}
