package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareAOF is like prepareAOFDir, with an append-only file that already
// holds the given commands.
func prepareAOF(t *testing.T, appendDirName, appendFilename, incrName string, commands ...[]string) (dir, incrPath string) {
	t.Helper()

	dir, incrPath = prepareAOFDir(t, appendDirName, appendFilename, incrName)
	var log strings.Builder
	for _, command := range commands {
		log.WriteString(encodeCommand(command...))
	}
	if err := os.WriteFile(incrPath, []byte(log.String()), 0o644); err != nil {
		t.Fatalf("writing the append-only file: %v", err)
	}
	return dir, incrPath
}

func TestCommandOfTheAppendOnlyFileIsReplayedAtStartup(t *testing.T) {
	dir, _ := prepareAOF(t, "my-aof-dir", "commands.aof", "random-name.1.incr.aof",
		[]string{"SET", "foo", "100"})
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir", "--appendfilename", "commands.aof")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\n100\r\n")
}

func TestReplayReadsTheFileOfTheManifestNotTheDefaultOne(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "random-name.1.incr.aof",
		[]string{"SET", "foo", "from-manifest"})
	// A file with the default name sits next to it, and must be ignored.
	decoy := filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof")
	if err := os.WriteFile(decoy, []byte(encodeCommand("SET", "foo", "from-default")), 0o644); err != nil {
		t.Fatalf("writing the decoy file: %v", err)
	}

	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$13\r\nfrom-manifest\r\n")
}

func TestReplayDoesNotLogTheCommandAgain(t *testing.T) {
	dir, incrPath := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "foo", "100"})
	before := readFile(t, incrPath)

	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	if got := readFile(t, incrPath); got != before {
		t.Errorf("append-only file holds %q after the replay, want it unchanged: %q", got, before)
	}

	// New writes are added after what was replayed.
	conn := dial(t, startServer(t, srv))
	do(t, conn, "SET", "bar", "200")
	expectReply(t, conn, "+OK\r\n")
	if got, want := readFile(t, incrPath), before+encodeCommand("SET", "bar", "200"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestNothingIsReplayedWhenAOFIsDisabled(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "foo", "100"})
	srv := newServerWithAOF(t, "--dir", dir)
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestReplayWithAnEmptyOrMissingFile(t *testing.T) {
	// Empty file.
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	conn := dial(t, startServer(t, newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	// The manifest names a file that isn't there.
	dir, incrPath := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	if err := os.Remove(incrPath); err != nil {
		t.Fatalf("removing the append-only file: %v", err)
	}
	conn = dial(t, startServer(t, newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	// Nothing at all: the first start.
	conn = dial(t, startServer(t, newServerWithAOF(t, "--dir", t.TempDir(), "--appendonly", "yes")))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestReplayedValueCanBeUsedLikeAnyOther(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "counter", "41"})
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":42\r\n")
	do(t, conn, "TYPE", "counter")
	expectReply(t, conn, "+string\r\n")
}

func TestReplayIsNotPropagatedToReplicasThatConnectLater(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "foo", "100"})
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	addr := startServer(t, srv)

	// Loading isn't writing: the replication stream starts empty.
	fields, _ := info(t, dial(t, addr), "replication")
	if got := fields["master_repl_offset"]; got != "0" {
		t.Errorf("master_repl_offset = %q after the replay, want 0", got)
	}
	if srv.loading {
		t.Errorf("the server still says it is loading after the replay")
	}
}

func TestRDBFileIsNotLoadedWhenAOFIsEnabled(t *testing.T) {
	dir, _ := prepareAOF(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof",
		[]string{"SET", "from_aof", "1"})
	if err := os.WriteFile(filepath.Join(dir, "dump.rdb"), buildRDB(rdbTestKey{key: "from_rdb", value: "1"}), 0o644); err != nil {
		t.Fatalf("writing the RDB file: %v", err)
	}

	cfg, err := parseConfig([]string{"--dir", dir, "--dbfilename", "dump.rdb", "--appendonly", "yes"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	// What the program does at startup.
	srv := NewServerWithConfig(cfg)
	if err := srv.loadRDBFile(); err != nil {
		t.Fatalf("loadRDBFile: %v", err)
	}
	if err := srv.setupAOF(); err != nil {
		t.Fatalf("setupAOF: %v", err)
	}
	t.Cleanup(func() { srv.closeAOF() })
	conn := dial(t, startServer(t, srv))

	// The append-only file is the source of truth.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("from_aof"))
}

func TestReplayFailsOnAnInvalidFile(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	// A valid command followed by something that isn't one.
	content := encodeCommand("SET", "foo", "100") + "*2\r\n+not a bulk string\r\n"
	if err := os.WriteFile(incrPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the append-only file: %v", err)
	}

	cfg, err := parseConfig([]string{"--dir", dir, "--appendonly", "yes"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	err = NewServerWithConfig(cfg).setupAOF()
	if err == nil || !strings.Contains(err.Error(), "replaying the append-only file custom.1.incr.aof") {
		t.Errorf("setupAOF returned %v, want an error about the replay", err)
	}
	// The file is left as it was, for someone to look at it.
	if got := readFile(t, incrPath); got != content {
		t.Errorf("the invalid append-only file was modified: %q", got)
	}
}

func TestReplayDropsACommandCutShortAtTheEndOfTheFile(t *testing.T) {
	complete := encodeCommand("SET", "foo", "100")
	partial := encodeCommand("SET", "bar", "200")

	for _, cut := range []int{1, 4, 10, len(partial) - 1} {
		dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
		if err := os.WriteFile(incrPath, []byte(complete+partial[:cut]), 0o644); err != nil {
			t.Fatalf("writing the append-only file: %v", err)
		}

		srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
		conn := dial(t, startServer(t, srv))

		// What was whole is replayed, the rest is gone.
		do(t, conn, "GET", "foo")
		expectReply(t, conn, "$3\r\n100\r\n")
		do(t, conn, "GET", "bar")
		expectReply(t, conn, "$-1\r\n")
		if got := readFile(t, incrPath); got != complete {
			t.Errorf("cut at %d: append-only file holds %q, want it truncated to %q", cut, got, complete)
		}

		// And the log stays valid as it grows.
		do(t, conn, "SET", "baz", "300")
		expectReply(t, conn, "+OK\r\n")
		if got, want := readFile(t, incrPath), complete+encodeCommand("SET", "baz", "300"); got != want {
			t.Errorf("cut at %d: append-only file holds %q, want %q", cut, got, want)
		}
	}
}
