package main

import "strings"

// This file implements users: the accounts clients are authenticated as, and
// the commands that deal with them.

// defaultUserName is the name of the user every server has from the start.
const defaultUserName = "default"

// user is an account on the server.
type user struct {
	name string
	// nopass is set for a user that has no password: anyone can be
	// authenticated as that user, whatever the password they give, if they
	// give one at all.
	nopass bool
}

// flags returns the flags of the user: short labels that describe how the
// account behaves.
//
//   - "nopass": the user has no password.
func (u *user) flags() []string {
	var flags []string
	if u.nopass {
		flags = append(flags, "nopass")
	}
	return flags
}

// cmdACL implements the ACL command, which is about the users of the server.
// Its first argument selects what it does:
//
//   - ACL WHOAMI replies with the name of the user the connection is
//     authenticated as, as a bulk string.
//   - ACL GETUSER username replies with the properties of the user, as an
//     array that alternates the names of the properties and their values:
//     "flags", then the array of the flags of the user. The reply is a null
//     bulk string if there is no such user.
//
// Subcommand names are case-insensitive.
func cmdACL(c *client, args []string) []byte {
	switch strings.ToUpper(args[0]) {
	case "WHOAMI":
		if len(args) != 1 {
			return errorReply("ERR wrong number of arguments for 'acl|whoami' command")
		}
		return bulkString(c.user.name)
	case "GETUSER":
		if len(args) != 2 {
			return errorReply("ERR wrong number of arguments for 'acl|getuser' command")
		}
		u, ok := c.server.users[args[1]]
		if !ok {
			return nullBulkString
		}
		return arrayReply(bulkString("flags"), bulkStringArray(u.flags()))
	default:
		return errorReply("ERR unknown subcommand '" + args[0] + "'. Try ACL HELP.")
	}
}
