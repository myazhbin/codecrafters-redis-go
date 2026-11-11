package main

import (
	"os"
	"strings"
	"testing"
)

// startServerWithArgs runs a server configured by the given command-line
// arguments and returns the address to dial.
func startServerWithArgs(t *testing.T, args ...string) string {
	t.Helper()

	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatalf("parseConfig(%q): %v", args, err)
	}
	return startServer(t, NewServerWithConfig(cfg))
}

func TestConfigGetDirAndDbFilename(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--dir", "/tmp/redis-files", "--dbfilename", "dump.rdb"))

	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, "*2\r\n$3\r\ndir\r\n$16\r\n/tmp/redis-files\r\n")
	do(t, conn, "CONFIG", "GET", "dbfilename")
	expectReply(t, conn, bulkArray("dbfilename", "dump.rdb"))
}

func TestConfigGetReflectsTheCommandLine(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--dbfilename", "snapshot file.rdb", "--dir", "/var/lib/my redis", "--port", "6380"))

	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, bulkArray("dir", "/var/lib/my redis"))
	do(t, conn, "CONFIG", "GET", "dbfilename")
	expectReply(t, conn, bulkArray("dbfilename", "snapshot file.rdb"))
	do(t, conn, "CONFIG", "GET", "port")
	expectReply(t, conn, bulkArray("port", "6380"))
}

func TestConfigGetDefaults(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "CONFIG", "GET", "dbfilename")
	expectReply(t, conn, bulkArray("dbfilename", "dump.rdb"))
	// The data directory defaults to where the server was started from.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting the working directory: %v", err)
	}
	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, bulkArray("dir", cwd))
	do(t, conn, "CONFIG", "GET", "port")
	expectReply(t, conn, bulkArray("port", "6379"))
}

func TestConfigGetIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--dir", "/tmp/Redis-Files"))

	// The name is reported in its canonical form; the value is left alone.
	for _, command := range [][]string{
		{"config", "get", "dir"},
		{"CONFIG", "GET", "DIR"},
		{"Config", "Get", "Dir"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, bulkArray("dir", "/tmp/Redis-Files"))
	}
}

func TestConfigGetUnknownParameter(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "CONFIG", "GET", "nosuchparameter")
	expectReply(t, conn, "*0\r\n")
}

func TestConfigGetSeveralParameters(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--dir", "/data", "--dbfilename", "db.rdb"))

	do(t, conn, "CONFIG", "GET", "dir", "dbfilename")
	expectReply(t, conn, bulkArray("dbfilename", "db.rdb", "dir", "/data"))
	// Unknown names are skipped and duplicates reported once.
	do(t, conn, "CONFIG", "GET", "dir", "nosuchparameter", "DIR")
	expectReply(t, conn, bulkArray("dir", "/data"))
}

func TestConfigGetPatterns(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--dir", "/data", "--dbfilename", "db.rdb", "--port", "6380"))

	tests := []struct {
		pattern string
		want    []string
	}{
		{"d*", []string{"dbfilename", "db.rdb", "dir", "/data"}},
		{"*i*", []string{"appenddirname", "appendonlydir", "appendfilename", "appendonly.aof", "dbfilename", "db.rdb", "dir", "/data"}},
		{"db*", []string{"dbfilename", "db.rdb"}},
		{"*name", []string{"appenddirname", "appendonlydir", "appendfilename", "appendonly.aof", "dbfilename", "db.rdb"}},
		{"db*name", []string{"dbfilename", "db.rdb"}},
		{"di?", []string{"dir", "/data"}},
		{"[dp]*r*", []string{"dir", "/data", "port", "6380"}},
		{"p*", []string{"port", "6380"}},
		{"x*", nil},
		{"[", nil}, // malformed
	}
	for _, tt := range tests {
		do(t, conn, "CONFIG", "GET", tt.pattern)
		expectReply(t, conn, bulkArray(tt.want...))
	}
}

func TestConfigErrors(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "CONFIG")
	expectReply(t, conn, "-ERR wrong number of arguments for 'config' command\r\n")
	do(t, conn, "CONFIG", "GET")
	expectReply(t, conn, "-ERR wrong number of arguments for 'config|get' command\r\n")
	do(t, conn, "CONFIG", "NOPE", "dir")
	expectReply(t, conn, "-ERR unknown subcommand 'NOPE'. Try CONFIG HELP.\r\n")
}

func TestParseConfigDirAndDbFilename(t *testing.T) {
	cfg, err := parseConfig([]string{"--dir", "/tmp/redis-files", "--dbfilename", "dump.rdb"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.dir != "/tmp/redis-files" || cfg.dbFilename != "dump.rdb" {
		t.Errorf("dir = %q, dbfilename = %q, want /tmp/redis-files and dump.rdb", cfg.dir, cfg.dbFilename)
	}
	// The other settings keep their defaults.
	if cfg.port != 6379 || cfg.replicaOf != nil {
		t.Errorf("port = %d, replicaOf = %v, want the defaults", cfg.port, cfg.replicaOf)
	}

	cfg, err = parseConfig([]string{"--DIR", "/a", "--DbFilename", "b", "--port", "6380", "--replicaof", "localhost 6379"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.dir != "/a" || cfg.dbFilename != "b" || cfg.port != 6380 || cfg.replicaOf == nil {
		t.Errorf("config = %+v, want every option to be taken into account", cfg)
	}
}

func TestParseConfigDirAndDbFilenameErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--dir"}, "needs a value"},
		{[]string{"--dbfilename"}, "needs a value"},
		{[]string{"--dir", "/tmp", "--dbfilename"}, "needs a value"},
		{[]string{"--dir", ""}, "can't be empty"},
		{[]string{"--dbfilename", ""}, "can't be empty"},
	}
	for _, tt := range tests {
		_, err := parseConfig(tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parseConfig(%q): error = %v, want it to mention %q", tt.args, err, tt.want)
		}
	}
}
