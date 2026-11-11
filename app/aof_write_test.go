package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prepareAOFDir creates, the way the tester does, an append-only directory in
// a new data directory: a manifest whose incremental entry names the file
// incrName, and that file, empty. It returns the data directory and the path
// of the file.
func prepareAOFDir(t *testing.T, appendDirName, appendFilename, incrName string) (dir, incrPath string) {
	t.Helper()

	dir = t.TempDir()
	appendDir := filepath.Join(dir, appendDirName)
	if err := os.MkdirAll(appendDir, 0o755); err != nil {
		t.Fatalf("creating the append-only directory: %v", err)
	}
	manifest := "file " + incrName + " seq 1 type i\n"
	if err := os.WriteFile(filepath.Join(appendDir, appendFilename+".manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	incrPath = filepath.Join(appendDir, incrName)
	if err := os.WriteFile(incrPath, nil, 0o644); err != nil {
		t.Fatalf("creating the append-only file: %v", err)
	}
	return dir, incrPath
}

// readFile returns the contents of the file at path.
func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

func TestWriteCommandIsAppendedToTheFileOfTheManifest(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "my-aof-dir", "commands.aof", "random-name.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir",
		"--appendfilename", "commands.aof", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")

	// The command is in the file by the time the client has its reply.
	if got, want := readFile(t, incrPath), "*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\n100\r\n"; got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}

	// The file with the default name isn't used, or even created.
	if _, err := os.Stat(filepath.Join(dir, "my-aof-dir", "commands.aof.1.incr.aof")); err == nil {
		t.Errorf("the default append-only file was created although the manifest names another one")
	}
}

func TestWriteCommandIsAppendedToTheDefaultFileWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")

	path := filepath.Join(dir, "appendonlydir", "appendonly.aof.1.incr.aof")
	if got, want := readFile(t, path), encodeCommand("SET", "foo", "100"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestAlwaysPolicyFlushesBeforeReplying(t *testing.T) {
	dir, _ := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	syncs := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.aofSyncs
	}
	if got := syncs(); got != 0 {
		t.Fatalf("the file was flushed %d times before any write", got)
	}

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	// The reply was only sent after the flush.
	if got := syncs(); got != 1 {
		t.Errorf("the file was flushed %d times by the time the client got its reply, want 1", got)
	}
}

func TestOtherPoliciesDoNotFlushOnEveryWrite(t *testing.T) {
	for _, policy := range []string{"everysec", "no"} {
		dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
		srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", policy)
		conn := dial(t, startServer(t, srv))

		do(t, conn, "SET", "foo", "100")
		expectReply(t, conn, "+OK\r\n")

		// The command is written all the same.
		if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "100"); got != want {
			t.Errorf("%s: append-only file holds %q, want %q", policy, got, want)
		}
		srv.mu.Lock()
		syncs := srv.aofSyncs
		srv.mu.Unlock()
		if syncs != 0 {
			t.Errorf("%s: the file was flushed %d times on a write, want 0", policy, syncs)
		}
	}
}

func TestNothingIsWrittenWhenAOFIsDisabled(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")

	if got := readFile(t, incrPath); got != "" {
		t.Errorf("append-only file holds %q although AOF persistence is disabled", got)
	}
}

func TestWriteCommandIsAppendedAfterWhatTheFileHolds(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	existing := encodeCommand("SET", "old", "1")
	if err := os.WriteFile(incrPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the append-only file: %v", err)
	}
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "new", "2")
	expectReply(t, conn, "+OK\r\n")

	if got, want := readFile(t, incrPath), existing+encodeCommand("SET", "new", "2"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestManifestWithSeveralEntries(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	// A base file comes first, as in the manifests Redis writes.
	manifest := "file custom.1.base.rdb seq 1 type b\nfile custom.1.incr.aof seq 1 type i\n"
	if err := os.WriteFile(filepath.Join(dir, "appendonlydir", "appendonly.aof.manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")

	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "100"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestFileOfTheManifestIsCreatedIfMissing(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	if err := os.Remove(incrPath); err != nil {
		t.Fatalf("removing the append-only file: %v", err)
	}
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	conn := dial(t, startServer(t, srv))

	do(t, conn, "SET", "foo", "100")
	expectReply(t, conn, "+OK\r\n")
	if got, want := readFile(t, incrPath), encodeCommand("SET", "foo", "100"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
}

func TestIncrFileOfManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		want     string
		found    bool
	}{
		{"single entry", "file appendonly.aof.1.incr.aof seq 1 type i\n", "appendonly.aof.1.incr.aof", true},
		{"no final newline", "file a.1.incr.aof seq 1 type i", "a.1.incr.aof", true},
		{"windows line endings", "file a.1.incr.aof seq 1 type i\r\n", "a.1.incr.aof", true},
		{"base and incremental", "file a.1.base.rdb seq 1 type b\nfile a.1.incr.aof seq 1 type i\n", "a.1.incr.aof", true},
		{"several incremental files", "file a.1.incr.aof seq 1 type i\nfile a.2.incr.aof seq 2 type i\n", "a.2.incr.aof", true},
		{"extra fields", "file a.1.incr.aof seq 1 type i startoffset 0 endoffset 120\n", "a.1.incr.aof", true},
		{"fields in another order", "type i seq 1 file a.1.incr.aof\n", "a.1.incr.aof", true},
		{"blank lines and comments", "\n# comment\nfile a.1.incr.aof seq 1 type i\n\n", "a.1.incr.aof", true},
		{"only a base file", "file a.1.base.rdb seq 1 type b\n", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		got, found, err := incrFileOfManifest(tt.manifest)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if got != tt.want || found != tt.found {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tt.name, got, found, tt.want, tt.found)
		}
	}
}

func TestIncrFileOfManifestErrors(t *testing.T) {
	for name, manifest := range map[string]string{
		"odd number of fields":    "file a.1.incr.aof seq 1 type\n",
		"no file name":            "seq 1 type i\n",
		"path instead of a name":  "file ../outside.aof seq 1 type i\n",
		"absolute path":           "file /etc/passwd seq 1 type i\n",
		"parent directory":        "file .. seq 1 type i\n",
		"subdirectory of the dir": "file sub/a.1.incr.aof seq 1 type i\n",
	} {
		if _, _, err := incrFileOfManifest(manifest); err == nil {
			t.Errorf("%s: incrFileOfManifest succeeded, want an error", name)
		}
	}
}

func TestSetupAOFFailsOnAnInvalidManifest(t *testing.T) {
	dir, _ := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	manifest := "file ../../outside.aof seq 1 type i\n"
	if err := os.WriteFile(filepath.Join(dir, "appendonlydir", "appendonly.aof.manifest"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}

	cfg, err := parseConfig([]string{"--dir", dir, "--appendonly", "yes"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	err = NewServerWithConfig(cfg).setupAOF()
	if err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("setupAOF returned %v, want an error about the manifest", err)
	}
}
