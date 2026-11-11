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

// cmdACL implements the ACL command, which is about the users of the server.
// Its first argument selects what it does:
//
//   - ACL WHOAMI replies with the name of the user the connection is
//     authenticated as, as a bulk string.
//
// Subcommand names are case-insensitive.
func cmdACL(c *client, args []string) []byte {
	switch strings.ToUpper(args[0]) {
	case "WHOAMI":
		if len(args) != 1 {
			return errorReply("ERR wrong number of arguments for 'acl|whoami' command")
		}
		return bulkString(c.user.name)
	default:
		return errorReply("ERR unknown subcommand '" + args[0] + "'. Try ACL HELP.")
	}
}
