package main

import "sort"

// This file implements the commands that are about keys in general, whatever
// the type of their value.

// cmdKeys implements KEYS pattern: it replies with the keys that match the
// glob-style pattern, in alphabetical order. The pattern "*" matches every
// key.
func cmdKeys(c *client, args []string) []byte {
	pattern := args[0]

	var matches []string
	for _, key := range c.server.store.keys() {
		if globMatch(pattern, key) {
			matches = append(matches, key)
		}
	}
	sort.Strings(matches)
	return bulkStringArray(matches)
}

// globMatch reports whether s matches the glob-style pattern, in the dialect
// Redis uses:
//
//   - matches any sequence of bytes, including none
//     ?        matches any single byte
//     [abc]    matches one of the bytes listed
//     [a-c]    matches one of the bytes of the range
//     [^abc]   matches any byte that isn't listed
//     \x       matches the byte x itself, even if it's special
//
// Unlike with file names, no byte is special in s: "*" matches "/" too.
func globMatch(pattern, s string) bool {
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			// Consecutive stars are the same as one.
			for len(pattern) > 1 && pattern[1] == '*' {
				pattern = pattern[1:]
			}
			if len(pattern) == 1 {
				return true
			}
			for i := 0; i <= len(s); i++ {
				if globMatch(pattern[1:], s[i:]) {
					return true
				}
			}
			return false

		case '?':
			if len(s) == 0 {
				return false
			}
			pattern, s = pattern[1:], s[1:]

		case '[':
			if len(s) == 0 {
				return false
			}
			rest, matched := matchClass(pattern[1:], s[0])
			if !matched {
				return false
			}
			pattern, s = rest, s[1:]

		default:
			if pattern[0] == '\\' && len(pattern) > 1 {
				pattern = pattern[1:]
			}
			if len(s) == 0 || pattern[0] != s[0] {
				return false
			}
			pattern, s = pattern[1:], s[1:]
		}
	}
	return len(s) == 0
}

// matchClass reports whether the byte b belongs to the character class at the
// start of class, which is what follows a "[" in a pattern. It returns what
// follows the class in the pattern as well. A class that is missing its "]"
// extends to the end of the pattern.
func matchClass(class string, b byte) (rest string, matched bool) {
	negated := len(class) > 0 && class[0] == '^'
	if negated {
		class = class[1:]
	}

	for len(class) > 0 && class[0] != ']' {
		switch {
		case class[0] == '\\' && len(class) > 1:
			if class[1] == b {
				matched = true
			}
			class = class[2:]
		case len(class) > 2 && class[1] == '-' && class[2] != ']':
			lo, hi := class[0], class[2]
			if lo > hi {
				lo, hi = hi, lo
			}
			if lo <= b && b <= hi {
				matched = true
			}
			class = class[3:]
		default:
			if class[0] == b {
				matched = true
			}
			class = class[1:]
		}
	}
	if len(class) > 0 {
		class = class[1:] // the closing bracket
	}
	return class, matched != negated
}
