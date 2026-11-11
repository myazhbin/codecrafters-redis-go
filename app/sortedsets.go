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

// remove takes member out of the set, and reports whether it was in it.
func (z *sortedSet) remove(member string) bool {
	score, ok := z.scores[member]
	if !ok {
		return false
	}
	i := z.position(scoredMember{member, score})
	z.ordered = slices.Delete(z.ordered, i, i+1)
	delete(z.scores, member)
	return true
}

// rank returns the rank of member: its position in the set, counting from 0,
// when the members are in order. It also reports whether the member is in
// the set at all.
func (z *sortedSet) rank(member string) (int, bool) {
	score, ok := z.scores[member]
	if !ok {
		return 0, false
	}
	return z.position(scoredMember{member, score}), true
}

// byRank returns the members whose rank goes from start to stop, both
// included, in order. See indexRange for what the two can be.
//
// The result shares its memory with the set and must not be modified.
func (z *sortedSet) byRank(start, stop int) []scoredMember {
	from, to := indexRange(len(z.ordered), start, stop)
	return z.ordered[from:to]
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

// formatScore writes a score the way Redis does in its replies:
//
//   - an infinite score is "inf" or "-inf";
//   - a score that is a whole number is written as an integer ("20"), as long
//     as a 64-bit integer holds it comfortably;
//   - any other score is written with the fewest digits that tell it apart
//     from every other 64-bit floating-point number ("30.1"). Scientific
//     notation is used when that takes fewer characters than spelling the
//     number out, which is for very small and very large scores ("1.5e-7",
//     "1e+21").
//
// What it returns is always parsed back by parseScore to the same score.
func formatScore(score float64) string {
	switch {
	case math.IsInf(score, 1):
		return "inf"
	case math.IsInf(score, -1):
		return "-inf"
	case score == 0:
		if math.Signbit(score) {
			return "-0"
		}
		return "0"
	}

	// Up to 2^62, a floating-point number without a fractional part is
	// exactly a 64-bit integer.
	const maxWhole = 1 << 62
	if score >= -maxWhole && score <= maxWhole {
		if n := int64(score); float64(n) == score {
			return strconv.FormatInt(n, 10)
		}
	}

	// The digits of the score, without leading or trailing zeros, and the
	// power of ten its first digit stands for: d.ddd × 10^exp.
	mantissa, rawExp, _ := strings.Cut(strconv.FormatFloat(math.Abs(score), 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(rawExp)
	// last is the power of ten the last digit stands for.
	last := exp - (len(digits) - 1)

	var b strings.Builder
	maxTrailingZeros := 7
	if score < 0 {
		b.WriteByte('-')
		maxTrailingZeros = 6
	}
	switch {
	case last >= 0 && exp < len(digits)+maxTrailingZeros:
		// A whole number, short enough to be written in full.
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", last))
	case last < 0 && (last > -7 || exp > -4 && exp < 4):
		// A number with a fractional part, short enough likewise.
		if point := len(digits) + last; point > 0 {
			b.WriteString(digits[:point] + "." + digits[point:])
		} else {
			b.WriteString("0." + strings.Repeat("0", -point) + digits)
		}
	default:
		b.WriteString(digits[:1])
		if len(digits) > 1 {
			b.WriteString("." + digits[1:])
		}
		if exp < 0 {
			b.WriteString("e-" + strconv.Itoa(-exp))
		} else {
			b.WriteString("e+" + strconv.Itoa(exp))
		}
	}
	return b.String()
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

// cmdZRank implements ZRANK key member: it replies with the rank of the
// member in the sorted set stored at key, which is its position, counting
// from 0, when the members are ordered by increasing score. Members that
// have the same score are ordered lexicographically.
//
// The reply is a null bulk string if the member isn't in the set, or if the
// key doesn't exist.
func cmdZRank(c *client, args []string) []byte {
	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return nullBulkString
	}
	rank, ok := z.rank(args[1])
	if !ok {
		return nullBulkString
	}
	return integerReply(rank)
}

// cmdZRange implements ZRANGE key start stop: it replies with the members of
// the sorted set stored at key whose rank goes from start to stop, both
// included, ordered by increasing score. Negative ranks count from the end
// of the set. A set that doesn't exist is treated as an empty one.
func cmdZRange(c *client, args []string) []byte {
	if len(args) > 3 {
		return errorReply("ERR syntax error")
	}
	start, startOK := parseIndex(args[1])
	stop, stopOK := parseIndex(args[2])
	if !startOK || !stopOK {
		return errorReply(errNotAnInteger)
	}

	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return bulkStringArray(nil)
	}
	members := z.byRank(start, stop)
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.member
	}
	return bulkStringArray(names)
}

// cmdZCard implements ZCARD key: it replies with the cardinality of the
// sorted set stored at key, which is the number of its members. It is 0 if
// the key doesn't exist.
func cmdZCard(c *client, args []string) []byte {
	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return integerReply(0)
	}
	return integerReply(len(z.ordered))
}

// cmdZScore implements ZSCORE key member: it replies with the score of the
// member in the sorted set stored at key, as a bulk string. See formatScore
// for how the score is written.
//
// The reply is a null bulk string if the member isn't in the set, or if the
// key doesn't exist.
func cmdZScore(c *client, args []string) []byte {
	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return nullBulkString
	}
	score, ok := z.scores[args[1]]
	if !ok {
		return nullBulkString
	}
	return bulkString(formatScore(score))
}

// cmdZRem implements ZREM key member [member ...]: it removes the members
// from the sorted set stored at key, and replies with the number of members
// that were removed: the ones that weren't in the set don't count. A set
// that loses its last member is deleted.
func cmdZRem(c *client, args []string) []byte {
	s := c.server
	key := args[0]

	z, err := s.store.getSortedSet(key)
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return integerReply(0)
	}

	removed := 0
	for _, member := range args[1:] {
		if z.remove(member) {
			removed++
		}
	}
	if removed > 0 {
		if len(z.ordered) == 0 {
			s.store.delete(key)
		} else {
			s.store.modified(key)
		}
		s.propagate(append([]string{"ZREM"}, args...)...)
	}
	return integerReply(removed)
}
