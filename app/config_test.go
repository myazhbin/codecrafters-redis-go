package main

import (
	"net"
	"strings"
	"testing"
)

func TestParseConfigDefaults(t *testing.T) {
	for _, args := range [][]string{nil, {}} {
		cfg, err := parseConfig(args)
		if err != nil {
			t.Fatalf("parseConfig(%q): unexpected error: %v", args, err)
		}
		if cfg.port != 6379 {
			t.Errorf("parseConfig(%q): port = %d, want 6379", args, cfg.port)
		}
	}
}

func TestParseConfigPort(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"--port", "6380"}, 6380},
		{[]string{"--port", "1"}, 1},
		{[]string{"--port", "65535"}, 65535},
		{[]string{"--port", "0"}, 0},
		{[]string{"--PORT", "7000"}, 7000},
		// As with Redis, the last occurrence of an option wins.
		{[]string{"--port", "7000", "--port", "7001"}, 7001},
	}
	for _, tt := range tests {
		cfg, err := parseConfig(tt.args)
		if err != nil {
			t.Errorf("parseConfig(%q): unexpected error: %v", tt.args, err)
			continue
		}
		if cfg.port != tt.want {
			t.Errorf("parseConfig(%q): port = %d, want %d", tt.args, cfg.port, tt.want)
		}
	}
}

func TestParseConfigErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string // a fragment of the error message
	}{
		{[]string{"--port"}, "needs a value"},
		{[]string{"--port", "abc"}, "invalid port"},
		{[]string{"--port", ""}, "invalid port"},
		{[]string{"--port", "-1"}, "invalid port"},
		{[]string{"--port", "65536"}, "invalid port"},
		{[]string{"--port", "6380.5"}, "invalid port"},
		{[]string{"--nope", "1"}, "unknown option"},
		{[]string{"6380"}, "unexpected argument"},
		{[]string{"--port", "6380", "extra"}, "unexpected argument"},
	}
	for _, tt := range tests {
		_, err := parseConfig(tt.args)
		if err == nil {
			t.Errorf("parseConfig(%q): expected an error", tt.args)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parseConfig(%q): error = %q, want it to mention %q", tt.args, err, tt.want)
		}
	}
}

func TestListenAddress(t *testing.T) {
	if got := defaultConfig().listenAddress(); got != "0.0.0.0:6379" {
		t.Errorf("default listen address = %q, want 0.0.0.0:6379", got)
	}
	if got := (config{port: 6380}).listenAddress(); got != "0.0.0.0:6380" {
		t.Errorf("listen address = %q, want 0.0.0.0:6380", got)
	}
}

// TestServerOnConfiguredPort checks the whole path from the command line to a
// server answering on the port that was asked for.
func TestServerOnConfiguredPort(t *testing.T) {
	// Ask the system for a port that is free, then release it for the server.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(probe.Addr().String())
	probe.Close()

	cfg, err := parseConfig([]string{"--port", port})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	l, err := net.Listen("tcp", cfg.listenAddress())
	if err != nil {
		t.Fatalf("listening on %s: %v", cfg.listenAddress(), err)
	}
	defer l.Close()
	go NewServer().Serve(l)

	conn := dial(t, net.JoinHostPort("127.0.0.1", port))
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}
