package main

import (
	"strings"
	"testing"
)

func TestConfigGetAOFOptionsFromFlags(t *testing.T) {
	conn := dial(t, startServerWithArgs(t,
		"--dir", "/tmp/redis-files",
		"--appendonly", "yes",
		"--appenddirname", "my-aof-dir",
		"--appendfilename", "commands.aof",
		"--appendfsync", "always",
	))

	do(t, conn, "CONFIG", "GET", "dir")
	expectReply(t, conn, bulkArray("dir", "/tmp/redis-files"))
	do(t, conn, "CONFIG", "GET", "appendonly")
	expectReply(t, conn, "*2\r\n$10\r\nappendonly\r\n$3\r\nyes\r\n")
	do(t, conn, "CONFIG", "GET", "appenddirname")
	expectReply(t, conn, bulkArray("appenddirname", "my-aof-dir"))
	do(t, conn, "CONFIG", "GET", "appendfilename")
	expectReply(t, conn, bulkArray("appendfilename", "commands.aof"))
	do(t, conn, "CONFIG", "GET", "appendfsync")
	expectReply(t, conn, bulkArray("appendfsync", "always"))
}

func TestAOFOptionsWithoutFlagKeepTheirDefault(t *testing.T) {
	conn := dial(t, startServerWithArgs(t, "--appendonly", "yes", "--appendfilename", "commands.aof"))

	do(t, conn, "CONFIG", "GET", "appendonly")
	expectReply(t, conn, bulkArray("appendonly", "yes"))
	do(t, conn, "CONFIG", "GET", "appendfilename")
	expectReply(t, conn, bulkArray("appendfilename", "commands.aof"))
	// Not given on the command line.
	do(t, conn, "CONFIG", "GET", "appenddirname")
	expectReply(t, conn, bulkArray("appenddirname", "appendonlydir"))
	do(t, conn, "CONFIG", "GET", "appendfsync")
	expectReply(t, conn, bulkArray("appendfsync", "everysec"))
	do(t, conn, "CONFIG", "GET", "dbfilename")
	expectReply(t, conn, bulkArray("dbfilename", "dump.rdb"))
}

func TestParseConfigAOFFlags(t *testing.T) {
	tests := []struct {
		args []string
		want func(cfg config) bool
	}{
		{[]string{"--appendonly", "yes"}, func(cfg config) bool { return cfg.appendOnly }},
		{[]string{"--appendonly", "no"}, func(cfg config) bool { return !cfg.appendOnly }},
		{[]string{"--appendonly", "YES"}, func(cfg config) bool { return cfg.appendOnly }},
		{[]string{"--APPENDONLY", "yes"}, func(cfg config) bool { return cfg.appendOnly }},
		{[]string{"--appenddirname", "aof"}, func(cfg config) bool { return cfg.appendDirName == "aof" }},
		{[]string{"--appendfilename", "file.aof"}, func(cfg config) bool { return cfg.appendFilename == "file.aof" }},
		{[]string{"--appendfilename", "with space.aof"}, func(cfg config) bool { return cfg.appendFilename == "with space.aof" }},
		{[]string{"--appendfsync", "always"}, func(cfg config) bool { return cfg.appendFsync == "always" }},
		{[]string{"--appendfsync", "everysec"}, func(cfg config) bool { return cfg.appendFsync == "everysec" }},
		{[]string{"--appendfsync", "no"}, func(cfg config) bool { return cfg.appendFsync == "no" }},
		{[]string{"--appendfsync", "ALWAYS"}, func(cfg config) bool { return cfg.appendFsync == "always" }},
		// Flags can come in any order, mixed with the other options.
		{[]string{"--appendfsync", "no", "--port", "6380", "--appendonly", "yes", "--dir", "/data"}, func(cfg config) bool {
			return cfg.appendFsync == "no" && cfg.port == 6380 && cfg.appendOnly && cfg.dir == "/data"
		}},
	}
	for _, tt := range tests {
		cfg, err := parseConfig(tt.args)
		if err != nil {
			t.Errorf("parseConfig(%q): unexpected error: %v", tt.args, err)
			continue
		}
		if !tt.want(cfg) {
			t.Errorf("parseConfig(%q) = %+v, the flags weren't taken into account", tt.args, cfg)
		}
	}
}

func TestParseConfigAOFFlagErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--appendonly"}, "needs a value"},
		{[]string{"--appenddirname"}, "needs a value"},
		{[]string{"--appendfilename"}, "needs a value"},
		{[]string{"--appendfsync"}, "needs a value"},
		{[]string{"--appendonly", "maybe"}, `must be "yes" or "no"`},
		{[]string{"--appendonly", ""}, `must be "yes" or "no"`},
		{[]string{"--appendfsync", "sometimes"}, `must be "always", "everysec" or "no"`},
		{[]string{"--appenddirname", ""}, "must be a file name"},
		{[]string{"--appenddirname", "a/b"}, "must be a file name"},
		{[]string{"--appenddirname", ".."}, "must be a file name"},
		{[]string{"--appendfilename", ""}, "must be a file name"},
		{[]string{"--appendfilename", "../outside.aof"}, "must be a file name"},
	}
	for _, tt := range tests {
		_, err := parseConfig(tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parseConfig(%q): error = %v, want it to mention %q", tt.args, err, tt.want)
		}
	}
}
