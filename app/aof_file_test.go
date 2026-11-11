package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendOnlyFileIsCreatedAtStartup(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir", "--appendfilename", "commands.aof")

	path := filepath.Join(dir, "my-aof-dir", "commands.aof.1.incr.aof")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the append-only file wasn't created: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("%s isn't a regular file", path)
	}
	if info.Size() != 0 {
		t.Errorf("the append-only file has %d bytes, want it to be empty", info.Size())
	}
}

func TestAppendOnlyFileHasADefaultName(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	if _, err := os.Stat(filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof")); err != nil {
		t.Errorf("the append-only file wasn't created with its default name: %v", err)
	}
}

func TestAppendOnlyFileIsNotCreatedWhenAOFIsDisabled(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithAOF(t, "--dir", dir, "--appendfilename", "commands.aof")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Errorf("%d entries were created in the data directory, want none", len(entries))
	}
	if srv.aof != nil {
		t.Errorf("an append-only file is open although AOF persistence is disabled")
	}
}

func TestExistingAppendOnlyFileIsNotTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof")
	content := encodeCommand("SET", "foo", "bar")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the file: %v", err)
	}
	if string(got) != content {
		t.Errorf("the append-only file now holds %q, want it untouched: %q", got, content)
	}
}

func TestIncrAOFPath(t *testing.T) {
	cfg, err := parseConfig([]string{"--dir", "/tmp/redis-data", "--appenddirname", "appendonlydir", "--appendfilename", "appendonly.aof"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if got, want := cfg.appendDir(), filepath.FromSlash("/tmp/redis-data/appendonlydir"); got != want {
		t.Errorf("append-only directory = %q, want %q", got, want)
	}
	if got, want := cfg.incrAOFPath(), filepath.FromSlash("/tmp/redis-data/appendonlydir/appendonly.aof.1.incr.aof"); got != want {
		t.Errorf("append-only file = %q, want %q", got, want)
	}
}

func TestSetupAOFFailsWhenTheFileCannotBeCreated(t *testing.T) {
	// A directory is in the way of the file.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof"), 0o755); err != nil {
		t.Fatalf("creating a directory: %v", err)
	}

	cfg, err := parseConfig([]string{"--dir", dir, "--appendonly", "yes"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if err := NewServerWithConfig(cfg).setupAOF(); err == nil {
		t.Errorf("setupAOF succeeded although a directory has the name of the file")
	}
}

func TestCloseAOF(t *testing.T) {
	srv := newServerWithAOF(t, "--dir", t.TempDir(), "--appendonly", "yes")

	if err := srv.closeAOF(); err != nil {
		t.Errorf("closeAOF: %v", err)
	}
	// Closing again, or closing a server without AOF, is harmless.
	if err := srv.closeAOF(); err != nil {
		t.Errorf("second closeAOF: %v", err)
	}
	if err := NewServer().closeAOF(); err != nil {
		t.Errorf("closeAOF without AOF: %v", err)
	}
}
