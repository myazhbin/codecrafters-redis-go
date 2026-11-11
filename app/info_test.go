package main

import (
	"net"
	"strings"
	"testing"
)

// info sends INFO with the given arguments and returns the fields of the
// reply, by name, along with the section headings found in it.
func info(t *testing.T, conn net.Conn, args ...string) (fields map[string]string, sections []string) {
	t.Helper()

	do(t, conn, append([]string{"INFO"}, args...)...)
	return parseInfo(t, readBulkString(t, conn))
}

// parseInfo splits an INFO report into its fields and its section headings.
func parseInfo(t *testing.T, report string) (fields map[string]string, sections []string) {
	t.Helper()

	fields = make(map[string]string)
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
			sections = append(sections, strings.TrimSpace(strings.TrimPrefix(line, "#")))
		default:
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("INFO line %q is not of the form name:value", line)
			}
			if _, dup := fields[name]; dup {
				t.Fatalf("INFO field %q is reported twice", name)
			}
			fields[name] = value
		}
	}
	return fields, sections
}

func TestInfoReplicationRoleIsMaster(t *testing.T) {
	conn := dial(t, startTestServer(t))

	fields, sections := info(t, conn, "replication")
	if got := fields["role"]; got != "master" {
		t.Errorf("role = %q, want master", got)
	}
	if len(sections) != 1 || sections[0] != "Replication" {
		t.Errorf("sections = %q, want only Replication", sections)
	}
}

func TestInfoIsABulkStringOfCRLFLines(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "INFO", "replication")
	report := readBulkString(t, conn)

	if !strings.HasPrefix(report, "# Replication\r\n") {
		t.Errorf("report = %q, want it to start with the section heading", report)
	}
	if !strings.Contains(report, "role:master\r\n") {
		t.Errorf("report = %q, want it to contain the line role:master", report)
	}
	if strings.Contains(strings.ReplaceAll(report, "\r\n", ""), "\n") {
		t.Errorf("report = %q, want every line to end with CRLF", report)
	}
}

func TestInfoSectionNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, section := range []string{"replication", "REPLICATION", "Replication"} {
		fields, _ := info(t, conn, section)
		if got := fields["role"]; got != "master" {
			t.Errorf("INFO %s: role = %q, want master", section, got)
		}
	}

	do(t, conn, "info", "replication")
	if report := readBulkString(t, conn); !strings.Contains(report, "role:master") {
		t.Errorf("lower-case command: report = %q, want role:master in it", report)
	}
}

func TestInfoWithoutSectionReportsEverything(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, args := range [][]string{nil, {"all"}, {"default"}, {"everything"}} {
		fields, sections := info(t, conn, args...)
		if got := fields["role"]; got != "master" {
			t.Errorf("INFO %q: role = %q, want master", args, got)
		}
		if len(sections) == 0 {
			t.Errorf("INFO %q: no section heading in the report", args)
		}
	}
}

func TestInfoUnknownSectionIsEmpty(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "INFO", "nosuchsection")
	expectReply(t, conn, "$0\r\n\r\n")
}

func TestInfoSeveralSections(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Unknown sections are skipped, and a section asked twice is reported
	// once.
	fields, sections := info(t, conn, "nosuchsection", "replication", "REPLICATION")
	if got := fields["role"]; got != "master" {
		t.Errorf("role = %q, want master", got)
	}
	if len(sections) != 1 {
		t.Errorf("sections = %q, want Replication once", sections)
	}
}

func TestInfoInsideTransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INFO", "replication")
	expectReply(t, conn, queued)
	do(t, conn, "EXEC")
	expectReply(t, conn, "*1\r\n")
	if report := readBulkString(t, conn); !strings.Contains(report, "role:master") {
		t.Errorf("report = %q, want role:master in it", report)
	}
}
