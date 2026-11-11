package main

import "strings"

// This file implements users: the accounts clients are authenticated as, and
// the commands that deal with them.

// defaultUserName is the name of the user every server has from the start.
const defaultUserName = "default"

// user is an account on the server.
type user struct {
	name string
}

// flags returns the flags of the user: short labels that describe how the
// account behaves.
func (u *user) flags() []string {
	return nil
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
