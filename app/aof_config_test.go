package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigGetAOFDefaults(t *testing.T) {
	conn := dial(t, startServerWithArgs(t))

	do(t, conn, "CONFIG", "GET", "appendonly")
	expectReply(t, conn, "*2\r\n$10\r\nappendonly\r\n$2\r\nno\r\n")
	do(t, conn, "CONFIG", "GET", "appenddirname")
	expectReply(t, conn, bulkArray("appenddirname", "appendonlydir"))
	do(t, conn, "CONFIG", "GET", "appendfilename")
	expectReply(t, conn, bulkArray("appendfilename", "appendonly.aof"))
	do(t, conn, "CONFIG", "GET", "appendfsync")
	expectReply(t, conn, bulkArray("appendfsync", "everysec"))
}

func TestConfigGetDirDefaultsToTheWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting the working directory: %v", err)
	}
	if !filepath.IsAbs(cwd) {
		t.Fatalf("working directory %q isn't absolute", cwd)
	}

	conn := dial(t, startServerWithArgs(t))
	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, bulkArray("dir", cwd))
}

func TestConfigGetDirFollowsTheWorkingDirectoryAtStartup(t *testing.T) {
	// EvalSymlinks: temporary directories can be behind a symbolic link, and
	// the working directory is reported with links resolved.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	t.Chdir(dir)

	conn := dial(t, startServerWithArgs(t))
	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, bulkArray("dir", dir))
}

func TestConfigGetAOFOptionsAreCaseInsensitive(t *testing.T) {
	conn := dial(t, startServerWithArgs(t))

	do(t, conn, "config", "get", "APPENDONLY")
	expectReply(t, conn, bulkArray("appendonly", "no"))
	do(t, conn, "CONFIG", "GET", "AppendFsync")
	expectReply(t, conn, bulkArray("appendfsync", "everysec"))
}

func TestConfigGetEveryAOFOptionAtOnce(t *testing.T) {
	conn := dial(t, startServerWithArgs(t))

	do(t, conn, "CONFIG", "GET", "append*")
	expectReply(t, conn, bulkArray(
		"appenddirname", "appendonlydir",
		"appendfilename", "appendonly.aof",
		"appendfsync", "everysec",
		"appendonly", "no",
	))
}

func TestDefaultConfigAOFOptions(t *testing.T) {
	cfg := defaultConfig()
	if cfg.appendOnly {
		t.Errorf("AOF persistence is enabled by default")
	}
	if cfg.appendDirName != "appendonlydir" || cfg.appendFilename != "appendonly.aof" || cfg.appendFsync != "everysec" {
		t.Errorf("AOF defaults = %q, %q, %q", cfg.appendDirName, cfg.appendFilename, cfg.appendFsync)
	}
	// The RDB settings are untouched.
	if cfg.dbFilename != "dump.rdb" || cfg.port != 6379 {
		t.Errorf("dbfilename = %q, port = %d, want dump.rdb and 6379", cfg.dbFilename, cfg.port)
	}
}

func TestYesNo(t *testing.T) {
	if yesNo(true) != "yes" || yesNo(false) != "no" {
		t.Errorf("yesNo = %q, %q, want yes and no", yesNo(true), yesNo(false))
	}
}
