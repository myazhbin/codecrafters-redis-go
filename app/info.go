package main

import (
	"strconv"
	"strings"
)

// This file implements INFO, which reports information about the server.

// infoSection is a group of related fields of the INFO report.
type infoSection struct {
	// name is the section's name as it appears in the report's headings.
	// Clients ask for a section using its name in any letter case.
	name string
	// fields returns the section's fields, in the order they are reported.
	fields func(s *Server) []infoField
}

// infoField is a line of the INFO report: a named value.
type infoField struct {
	name, value string
}

// infoSections lists the sections of the INFO report, in the order they are
// reported.
var infoSections = []infoSection{
	{name: "Replication", fields: (*Server).replicationInfo},
}

// replicationInfo returns the fields of the "Replication" section of INFO.
func (s *Server) replicationInfo() []infoField {
	// Redis still calls replicas "slaves" in this report.
	role := "master"
	if s.isReplica() {
		role = "slave"
	}
	// A replica reports how far into its master's stream it is.
	offset := s.replOffset
	if s.isReplica() {
		offset = s.masterOffset
	}
	return []infoField{
		{"role", role},
		{"master_replid", s.replID},
		{"master_repl_offset", strconv.FormatInt(offset, 10)},
	}
}

// cmdInfo implements INFO [section ...]: it replies with a bulk string that
// describes the server, made of lines of the form "name:value" grouped in
// sections. Each section starts with a "# Name" heading and sections are
// separated by an empty line.
//
// Without arguments every section is reported. Otherwise only the requested
// ones are, and names that match no section are ignored. "all", "default"
// and "everything" stand for all the sections.
func cmdInfo(c *client, args []string) []byte {
	wanted := func(section infoSection) bool {
		if len(args) == 0 {
			return true
		}
		for _, arg := range args {
			switch strings.ToLower(arg) {
			case "all", "default", "everything", strings.ToLower(section.name):
				return true
			}
		}
		return false
	}

	var b strings.Builder
	for _, section := range infoSections {
		if !wanted(section) {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString("# " + section.name + "\r\n")
		for _, field := range section.fields(c.server) {
			b.WriteString(field.name + ":" + field.value + "\r\n")
		}
	}
	return bulkString(b.String())
}
