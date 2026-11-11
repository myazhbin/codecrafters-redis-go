package main

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
)

// This file implements sorted sets and the commands that operate on them.

// sortedSet is the value of a sorted set key: a set of members, which are
// strings, each of them with a score. A member is in the set once, with one
// score.
//
// The members are kept in order: by increasing score and, for the members
// that have the same score, in the lexicographical order of their bytes.
// Keeping them in a slice makes looking a position up cheap (a binary search)
// and reading a range of positions trivial; adding or removing a member
// costs a move of the members that come after it.
type sortedSet struct {
	// scores holds the score of each member.
	scores map[string]float64
	// ordered holds the members and their scores, in order.
	ordered []scoredMember
}

// scoredMember is a member of a sorted set along with its score.
type scoredMember struct {
	member string
	score  float64
}

// compareScoredMembers tells which of two members of a sorted set comes
// first: it returns a negative number if a does, a positive one if b does and
// 0 if they are the same member with the same score.
func compareScoredMembers(a, b scoredMember) int {
	if c := cmp.Compare(a.score, b.score); c != 0 {
		return c
	}
	return strings.Compare(a.member, b.member)
}

func newSortedSet() *sortedSet {
	return &sortedSet{scores: make(map[string]float64)}
}

// position returns the index of m among the ordered members or, if m isn't
// one of them, the index it would be inserted at.
func (z *sortedSet) position(m scoredMember) int {
	i, _ := slices.BinarySearchFunc(z.ordered, m, compareScoredMembers)
	return i
}

// add puts member in the set with the given score. A member that is already
// in the set stays in it, and is given the score instead of the one it had.
// It reports whether the member was added, and whether the set changed at
// all, which it doesn't when the member already had that score.
func (z *sortedSet) add(member string, score float64) (added, changed bool) {
	old, exists := z.scores[member]
	if exists {
		if old == score {
			return false, false
		}
		// The member moves to where its new score puts it.
		i := z.position(scoredMember{member, old})
		z.ordered = slices.Delete(z.ordered, i, i+1)
	}

	m := scoredMember{member, score}
	z.scores[member] = score
	z.ordered = slices.Insert(z.ordered, z.position(m), m)
	return !exists, true
}

// parseScore parses the score of a member of a sorted set: a 64-bit
// floating-point number. It can be infinite ("inf", "+inf", "-inf"), but it
// has to be a number.
func parseScore(s string) (float64, bool) {
	// strconv follows the syntax of Go, in which underscores can separate
	// the digits of a number. Not in a score.
	if strings.Contains(s, "_") {
		return 0, false
	}
	score, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(score) {
		return 0, false
	}
	return score, true
}

// cmdZAdd implements ZADD key score member [score member ...]: it adds the
// members to the sorted set stored at key with the scores they are given,
// creating the set if needed. A member that is already in the set is given
// its new score, which moves it to the position that goes with it.
//
// The reply is the number of members that were added: the ones that only
// changed score don't count.
func cmdZAdd(c *client, args []string) []byte {
	s := c.server
	key, pairs := args[0], args[1:]
	if len(pairs)%2 != 0 {
		return errorReply("ERR syntax error")
	}

	// Every score is checked before anything is changed: the command is
	// applied in full or not at all.
	scores := make([]float64, len(pairs)/2)
	for i := range scores {
		score, ok := parseScore(pairs[2*i])
		if !ok {
			return errorReply("ERR value is not a valid float")
		}
		scores[i] = score
	}

	z, err := s.store.getOrCreateSortedSet(key)
	if err != nil {
		return errorReply(err.Error())
	}
	added, modified := 0, false
	for i, score := range scores {
		wasAdded, changed := z.add(pairs[2*i+1], score)
		if wasAdded {
			added++
		}
		modified = modified || changed
	}
	if modified {
		s.store.modified(key)
		s.propagate(append([]string{"ZADD"}, args...)...)
	}
	return integerReply(added)
}
