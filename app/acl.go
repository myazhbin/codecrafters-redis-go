package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
)

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
	// passwords holds the passwords of the user, in the order they were
	// added. The passwords themselves aren't kept, only their SHA-256 hash,
	// written in hexadecimal.
	passwords []string
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

// hashPassword returns what is kept of a password: its SHA-256 hash, written
// in lower-case hexadecimal. A password can't be found out from its hash, so
// that reading the list of the users doesn't give anyone's password away.
func hashPassword(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}

// accepts reports whether the password is one the user can be authenticated
// with: any password is, for a user that has the nopass flag, and the
// passwords that were added to the user otherwise.
//
// The time the comparison takes doesn't depend on how close the password is
// to a right one, nor on which of the user's passwords it is.
func (u *user) accepts(password string) bool {
	if u.nopass {
		return true
	}
	hash := []byte(hashPassword(password))
	accepted := 0
	for _, known := range u.passwords {
		accepted |= subtle.ConstantTimeCompare(hash, []byte(known))
	}
	return accepted == 1
}

// applyRule changes the user according to a rule of ACL SETUSER:
//
//   - ">password" adds the password to the ones the user can be
//     authenticated with. A user that has a password isn't without password
//     anymore: the nopass flag is cleared.
//   - "<password" removes the password, which the user must have.
//   - "nopass" removes every password and sets the nopass flag: any password
//     will do.
//   - "resetpass" removes every password and clears the nopass flag: no
//     password will do, until one is added.
//
// The names of the rules that are words are case-insensitive.
func (u *user) applyRule(rule string) error {
	switch {
	case strings.HasPrefix(rule, ">"):
		if hash := hashPassword(rule[1:]); !slices.Contains(u.passwords, hash) {
			u.passwords = append(u.passwords, hash)
		}
		u.nopass = false
	case strings.HasPrefix(rule, "<"):
		i := slices.Index(u.passwords, hashPassword(rule[1:]))
		if i == -1 {
			return errors.New("The password you are trying to remove from the user does not exist")
		}
		u.passwords = slices.Delete(u.passwords, i, i+1)
	case strings.EqualFold(rule, "nopass"):
		u.passwords = nil
		u.nopass = true
	case strings.EqualFold(rule, "resetpass"):
		u.passwords = nil
		u.nopass = false
	default:
		return errors.New("Syntax error")
	}
	return nil
}

// cmdACL implements the ACL command, which is about the users of the server.
// Its first argument selects what it does:
//
//   - ACL WHOAMI replies with the name of the user the connection is
//     authenticated as, as a bulk string.
//   - ACL GETUSER username replies with the properties of the user, as an
//     array that alternates the names of the properties and their values:
//     "flags", then the array of the flags of the user, and "passwords",
//     then the array of the hashes of its passwords. The reply is a null
//     bulk string if there is no such user.
//   - ACL SETUSER username [rule ...] changes the user according to the
//     rules, in the order they are given: see applyRule for what they can
//     be. A user that doesn't exist is created first, without password and
//     without the nopass flag. If one of the rules is invalid none of them
//     is applied.
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
		return arrayReply(
			bulkString("flags"), bulkStringArray(u.flags()),
			bulkString("passwords"), bulkStringArray(u.passwords),
		)
	case "SETUSER":
		if len(args) < 2 {
			return errorReply("ERR wrong number of arguments for 'acl|setuser' command")
		}
		name, rules := args[1], args[2:]
		if strings.ContainsAny(name, " \t\n\v\f\r\x00") {
			return errorReply("ERR Usernames can't contain spaces or null characters")
		}

		// The rules are applied to a copy of the user, which only takes its
		// place once all of them have been.
		updated := user{name: name}
		existing, exists := c.server.users[name]
		if exists {
			updated = *existing
			updated.passwords = slices.Clone(existing.passwords)
		}
		for _, rule := range rules {
			if err := updated.applyRule(rule); err != nil {
				return errorReply("ERR Error in ACL SETUSER modifier '" + rule + "': " + err.Error())
			}
		}
		// A user that exists is changed in place: the clients that are
		// authenticated as that user are concerned too.
		if exists {
			*existing = updated
		} else {
			c.server.users[name] = &updated
		}
		return simpleString("OK")
	default:
		return errorReply("ERR unknown subcommand '" + args[0] + "'. Try ACL HELP.")
	}
}

// cmdAuth implements AUTH [username] password: it authenticates the
// connection as the user, if the password is one of the user's. Without
// username, the user is the default one.
//
// The reply is OK, or an error that doesn't tell whether it is the user or
// the password that is wrong. The connection stays as it was after a failed
// attempt.
func cmdAuth(c *client, args []string) []byte {
	if len(args) > 2 {
		return errorReply("ERR syntax error")
	}
	s := c.server

	name, password := defaultUserName, args[0]
	switch {
	case len(args) == 2:
		name, password = args[0], args[1]
	case s.defaultUser.nopass:
		// A password alone is the way to authenticate on a server that is
		// protected by a single password. This one isn't.
		return errorReply("ERR AUTH <password> called without any password configured for the default user. Are you sure your configuration is correct?")
	}

	u, ok := s.users[name]
	if !ok || !u.accepts(password) {
		return errorReply("WRONGPASS invalid username-password pair or user is disabled.")
	}
	c.user = u
	return simpleString("OK")
}
