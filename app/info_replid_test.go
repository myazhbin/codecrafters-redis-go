package main

import (
	"regexp"
	"strings"
	"testing"
)

var replicationIDPattern = regexp.MustCompile(`^[0-9a-zA-Z]{40}$`)

func TestInfoReplicationIDAndOffset(t *testing.T) {
	conn := dial(t, startTestServer(t))

	fields, _ := info(t, conn, "replication")
	if got := fields["role"]; got != "master" {
		t.Errorf("role = %q, want master", got)
	}
	if got := fields["master_replid"]; !replicationIDPattern.MatchString(got) {
		t.Errorf("master_replid = %q, want 40 alphanumeric characters", got)
	}
	if got := fields["master_repl_offset"]; got != "0" {
		t.Errorf("master_repl_offset = %q, want 0", got)
	}
}

func TestInfoReplicationFieldsAreSeparateLines(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "INFO", "replication")
	report := readBulkString(t, conn)
	want := "# Replication\r\nrole:master\r\nmaster_replid:" + srv.replID + "\r\nmaster_repl_offset:0\r\n"
	if report != want {
		t.Errorf("report = %q, want %q", report, want)
	}
}

func TestInfoReplicationIDIsStable(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	other := dial(t, addr)

	first, _ := info(t, conn, "replication")
	// Writing to the server doesn't change its identity...
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	second, _ := info(t, conn, "replication")
	// ...and every client is told the same.
	third, _ := info(t, other, "replication")

	if first["master_replid"] != second["master_replid"] || first["master_replid"] != third["master_replid"] {
		t.Errorf("master_replid changed: %q, %q, %q",
			first["master_replid"], second["master_replid"], third["master_replid"])
	}
}

func TestEachServerHasItsOwnReplicationID(t *testing.T) {
	a, b := NewServer(), NewServer()
	if a.replID == b.replID {
		t.Errorf("two servers share the replication ID %q", a.replID)
	}
}

func TestNewReplicationID(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		id := newReplicationID()
		if !replicationIDPattern.MatchString(id) {
			t.Fatalf("replication ID %q is not made of 40 alphanumeric characters", id)
		}
		if id != strings.ToLower(id) {
			t.Fatalf("replication ID %q is not lower-case", id)
		}
		if seen[id] {
			t.Fatalf("replication ID %q was generated twice", id)
		}
		seen[id] = true
	}
}

func TestInfoReplicationIDAndOffsetOnAReplica(t *testing.T) {
	conn := dial(t, startReplicaTestServer(t))

	fields, _ := info(t, conn, "replication")
	if got := fields["role"]; got != "slave" {
		t.Errorf("role = %q, want slave", got)
	}
	if got := fields["master_replid"]; !replicationIDPattern.MatchString(got) {
		t.Errorf("master_replid = %q, want 40 alphanumeric characters", got)
	}
	if got := fields["master_repl_offset"]; got != "0" {
		t.Errorf("master_repl_offset = %q, want 0", got)
	}
}
