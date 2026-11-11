package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestIsCreatedAtStartup(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appenddirname", "my-aof-dir", "--appendfilename", "commands.aof")

	manifest, err := os.ReadFile(filepath.Join(dir, "my-aof-dir", "commands.aof.manifest"))
	if err != nil {
		t.Fatalf("the manifest wasn't created: %v", err)
	}
	// Fields are separated by single spaces and the line ends with a
	// newline.
	if want := "file commands.aof.1.incr.aof seq 1 type i\n"; string(manifest) != want {
		t.Errorf("manifest = %q, want %q", manifest, want)
	}

	// The file it describes is there too.
	if _, err := os.Stat(filepath.Join(dir, "my-aof-dir", "commands.aof.1.incr.aof")); err != nil {
		t.Errorf("the append-only file listed in the manifest doesn't exist: %v", err)
	}
}

func TestManifestHasADefaultName(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	manifest, err := os.ReadFile(filepath.Join(dir, "appendonlydir", "appendonly.aof.manifest"))
	if err != nil {
		t.Fatalf("the manifest wasn't created with its default name: %v", err)
	}
	if want := "file appendonly.aof.1.incr.aof seq 1 type i\n"; string(manifest) != want {
		t.Errorf("manifest = %q, want %q", manifest, want)
	}
}

func TestAppendOnlyDirectoryHoldsExactlyTheFileAndItsManifest(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	entries, err := os.ReadDir(filepath.Join(dir, "appendonlydir"))
	if err != nil {
		t.Fatalf("reading the append-only directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	// ReadDir returns the entries sorted by name.
	if len(names) != 2 || names[0] != "appendonly.aof.1.incr.aof" || names[1] != "appendonly.aof.manifest" {
		t.Errorf("append-only directory holds %q, want the append-only file and the manifest", names)
	}
}

func TestManifestIsNotCreatedWhenAOFIsDisabled(t *testing.T) {
	dir := t.TempDir()
	newServerWithAOF(t, "--dir", dir, "--appenddirname", "my-aof-dir")

	if _, err := os.Stat(filepath.Join(dir, "my-aof-dir")); err == nil {
		t.Errorf("the append-only directory was created although AOF persistence is disabled")
	}
}

func TestExistingManifestIsKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "appendonlydir", "appendonly.aof.manifest")
	existing := "file appendonly.aof.1.base.rdb seq 1 type b\nfile appendonly.aof.1.incr.aof seq 1 type i\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}

	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	manifest, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	if string(manifest) != existing {
		t.Errorf("manifest = %q, want it untouched: %q", manifest, existing)
	}
}

func TestManifestIsTheSameAfterARestart(t *testing.T) {
	dir := t.TempDir()
	first := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")
	first.closeAOF()
	newServerWithAOF(t, "--dir", dir, "--appendonly", "yes")

	manifest, err := os.ReadFile(filepath.Join(dir, "appendonlydir", "appendonly.aof.manifest"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	// One line, not one per start.
	if want := "file appendonly.aof.1.incr.aof seq 1 type i\n"; string(manifest) != want {
		t.Errorf("manifest = %q, want %q", manifest, want)
	}
}

func TestCreateFileIfMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")

	if err := createFileIfMissing(path, "first"); err != nil {
		t.Fatalf("creating the file: %v", err)
	}
	if err := createFileIfMissing(path, "second"); err != nil {
		t.Fatalf("on an existing file: %v", err)
	}
	if content, _ := os.ReadFile(path); string(content) != "first" {
		t.Errorf("file holds %q, want the contents it was created with", content)
	}

	if err := createFileIfMissing(filepath.Join(t.TempDir(), "missing-dir", "file"), "x"); err == nil {
		t.Errorf("creating a file in a directory that doesn't exist succeeded")
	}
}
