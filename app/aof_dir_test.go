package main

import (
	"os"
	"path/filepath"
	"testing"
)

// newServerWithAOF returns a server configured by the command-line arguments
// whose AOF persistence has been set up, like the program does at startup.
func newServerWithAOF(t *testing.T, args ...string) *Server {
	t.Helper()

	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatalf("parseConfig(%q): %v", args, err)
	}
	srv := NewServerWithConfig(cfg)
	if err := srv.setupAOF(); err != nil {
		t.Fatalf("setupAOF: %v", err)
	}
	return srv
}

// isDir reports whether path is an existing directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestAppendOnlyDirectoryIsCreatedAtStartup(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir", "--appendfilename", "commands.aof")

	// The directory is there before the server serves anyone.
	if !isDir(filepath.Join(dir, "my-aof-dir")) {
		t.Errorf("the append-only directory wasn't created in %s", dir)
	}
}

func TestAppendOnlyDirectoryHasADefaultName(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	if !isDir(filepath.Join(dir, "appendonlydir")) {
		t.Errorf("the append-only directory wasn't created with its default name")
	}
}

func TestAppendOnlyDirectoryIsNotCreatedWhenAOFIsDisabled(t *testing.T) {
	for _, args := range [][]string{
		{"--appenddirname", "my-aof-dir"},
		{"--appendonly", "no", "--appenddirname", "my-aof-dir"},
	} {
		dir := t.TempDir()
		newServerWithAOF(t, append([]string{"--dir", dir}, args...)...)

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Errorf("started with %q: %d entries were created in the data directory, want none", args, len(entries))
		}
	}
}

func TestExistingAppendOnlyDirectoryIsKept(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "appendonlydir", "existing-file")
	if err := os.MkdirAll(filepath.Dir(existing), 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(existing, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	// Starting twice in a row is fine too.
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	if content, err := os.ReadFile(existing); err != nil || string(content) != "keep me" {
		t.Errorf("the file of the existing directory was touched: %q, %v", content, err)
	}
}

func TestAppendOnlyDirectoryIsCreatedWithItsParents(t *testing.T) {
	// The data directory itself doesn't exist yet.
	dir := filepath.Join(t.TempDir(), "data", "redis")
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	if !isDir(filepath.Join(dir, "appendonlydir")) {
		t.Errorf("the append-only directory wasn't created under %s", dir)
	}
}

func TestSetupAOFFailsWhenTheDirectoryCannotBeCreated(t *testing.T) {
	// A file is in the way of the directory.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "appendonlydir"), []byte("a file"), 0o644); err != nil {
		t.Fatalf("writing a file: %v", err)
	}

	cfg, err := parseConfig([]string{"--dir", dir, "--appendonly", "yes"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if err := NewServerWithConfig(cfg).setupAOF(); err == nil {
		t.Errorf("setupAOF succeeded although a file has the name of the directory")
	}
}

func TestServerWithAOFServesClients(t *testing.T) {
	srv := newServerWithAOF(t, "--dir", t.TempDir(), "--appendonly", "yes")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "CONFIG", "GET", "appendonly")
	expectReply(t, conn, bulkArray("appendonly", "yes"))
}
