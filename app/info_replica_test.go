package main

import (
	"strings"
	"testing"
)

// startReplicaTestServer runs a server configured as the replica of a master
// at an address nobody listens on, and returns the address to dial.
func startReplicaTestServer(t *testing.T) string {
	t.Helper()

	cfg := defaultConfig()
	cfg.replicaOf = &masterAddress{host: "localhost", port: 6379}
	return startServer(t, NewServerWithConfig(cfg))
}

func TestInfoReplicationRoleIsSlaveOnAReplica(t *testing.T) {
	conn := dial(t, startReplicaTestServer(t))

	fields, _ := info(t, conn, "replication")
	if got := fields["role"]; got != "slave" {
		t.Errorf("role = %q, want slave", got)
	}
}

func TestInfoRoleOnAReplicaWithoutSection(t *testing.T) {
	conn := dial(t, startReplicaTestServer(t))

	do(t, conn, "INFO")
	report := readBulkString(t, conn)
	if !strings.Contains(report, "role:slave\r\n") {
		t.Errorf("report = %q, want the line role:slave in it", report)
	}
	if strings.Contains(report, "role:master") {
		t.Errorf("report = %q, a replica must not claim to be a master", report)
	}
}

func TestInfoRoleFollowsTheCommandLine(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{nil, "master"},
		{[]string{"--port", "6380"}, "master"},
		{[]string{"--port", "6380", "--replicaof", "localhost 6379"}, "slave"},
		{[]string{"--replicaof", "localhost 6379"}, "slave"},
		{[]string{"--replicaof", "127.0.0.1 7000", "--port", "6381"}, "slave"},
	}
	for _, tt := range tests {
		cfg, err := parseConfig(tt.args)
		if err != nil {
			t.Fatalf("parseConfig(%q): %v", tt.args, err)
		}
		conn := dial(t, startServer(t, NewServerWithConfig(cfg)))
		fields, _ := info(t, conn, "replication")
		if got := fields["role"]; got != tt.want {
			t.Errorf("started with %q: role = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestParseConfigReplicaOf(t *testing.T) {
	tests := []struct {
		args     []string
		wantHost string
		wantPort int
	}{
		{[]string{"--replicaof", "localhost 6379"}, "localhost", 6379},
		{[]string{"--replicaof", "127.0.0.1 7000"}, "127.0.0.1", 7000},
		{[]string{"--replicaof", "  localhost   6379 "}, "localhost", 6379},
		// The host and the port can also be separate arguments.
		{[]string{"--replicaof", "localhost", "6379"}, "localhost", 6379},
		{[]string{"--REPLICAOF", "localhost 6379"}, "localhost", 6379},
		{[]string{"--port", "6380", "--replicaof", "localhost 6379"}, "localhost", 6379},
		{[]string{"--replicaof", "localhost", "6379", "--port", "6380"}, "localhost", 6379},
	}
	for _, tt := range tests {
		cfg, err := parseConfig(tt.args)
		if err != nil {
			t.Errorf("parseConfig(%q): unexpected error: %v", tt.args, err)
			continue
		}
		if cfg.replicaOf == nil {
			t.Errorf("parseConfig(%q): no master configured", tt.args)
			continue
		}
		if cfg.replicaOf.host != tt.wantHost || cfg.replicaOf.port != tt.wantPort {
			t.Errorf("parseConfig(%q): master = %s %d, want %s %d",
				tt.args, cfg.replicaOf.host, cfg.replicaOf.port, tt.wantHost, tt.wantPort)
		}
	}
}

func TestParseConfigReplicaOfKeepsThePort(t *testing.T) {
	cfg, err := parseConfig([]string{"--port", "6380", "--replicaof", "localhost 6379"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	// The master's port and the server's own port are two different things.
	if cfg.port != 6380 {
		t.Errorf("port = %d, want 6380", cfg.port)
	}
}

func TestParseConfigWithoutReplicaOfIsAMaster(t *testing.T) {
	cfg, err := parseConfig([]string{"--port", "6380"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.replicaOf != nil {
		t.Errorf("master = %v, want none", cfg.replicaOf)
	}
}

func TestParseConfigReplicaOfErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--replicaof"}, "needs a value"},
		{[]string{"--replicaof", "localhost"}, "needs a master host and port"},
		{[]string{"--replicaof", ""}, "needs a master host and port"},
		{[]string{"--replicaof", "localhost 6379 extra"}, "needs a master host and port"},
		{[]string{"--replicaof", "localhost", "--port", "6380"}, "needs a master host and port"},
		{[]string{"--replicaof", "localhost abc"}, "invalid port"},
		{[]string{"--replicaof", "localhost 70000"}, "invalid port"},
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

func TestMasterAddressString(t *testing.T) {
	if got := (masterAddress{host: "localhost", port: 6379}).String(); got != "localhost:6379" {
		t.Errorf("address = %q, want localhost:6379", got)
	}
	if got := (masterAddress{host: "::1", port: 6379}).String(); got != "[::1]:6379" {
		t.Errorf("address = %q, want [::1]:6379", got)
	}
}
