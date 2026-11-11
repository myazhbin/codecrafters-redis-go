package main

import (
	"strconv"
	"testing"
)

func TestZRangeWithNegativeIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "20.0", "foo")
	zadd(t, conn, "zset_key", "30.1", "bar")
	zadd(t, conn, "zset_key", "40.2", "baz")
	zadd(t, conn, "zset_key", "25.0", "paz")
	zadd(t, conn, "zset_key", "25.0", "caz")

	// Ordered by score, the members are foo, caz, paz, bar and baz.
	do(t, conn, "ZRANGE", "zset_key", "2", "-1")
	expectReply(t, conn, "*3\r\n$3\r\npaz\r\n$3\r\nbar\r\n$3\r\nbaz\r\n")

	// -1 is the last member, -2 the one before, and so on.
	expectZRange(t, conn, "zset_key", 0, -1, "foo", "caz", "paz", "bar", "baz")
	expectZRange(t, conn, "zset_key", -1, -1, "baz")
	expectZRange(t, conn, "zset_key", -2, -1, "bar", "baz")
	expectZRange(t, conn, "zset_key", -5, -1, "foo", "caz", "paz", "bar", "baz")
	expectZRange(t, conn, "zset_key", -5, -5, "foo")
	expectZRange(t, conn, "zset_key", -4, -2, "caz", "paz", "bar")
	expectZRange(t, conn, "zset_key", 0, -2, "foo", "caz", "paz", "bar")
	expectZRange(t, conn, "zset_key", 1, -3, "caz", "paz")
	expectZRange(t, conn, "zset_key", -3, 4, "paz", "bar", "baz")
	expectZRange(t, conn, "zset_key", -3, 3, "paz", "bar")
}

func TestZRangeLastMembersAndAllButTheLast(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "racer_scores", "8.5", "Sam-Bodden")
	zadd(t, conn, "racer_scores", "10.2", "Royce")
	zadd(t, conn, "racer_scores", "6.1", "Ford")
	zadd(t, conn, "racer_scores", "14.9", "Prickett")
	zadd(t, conn, "racer_scores", "10.2", "Ben")

	// The last two members.
	expectZRange(t, conn, "racer_scores", -2, -1, "Royce", "Prickett")
	// All of them but the last two.
	expectZRange(t, conn, "racer_scores", 0, -3, "Ford", "Sam-Bodden", "Ben")
}

func TestZRangeNegativeStartBeforeTheStartOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	// A negative index that points before the first member is the same as 0.
	expectZRange(t, conn, "zset_key", -6, -1, "baz", "caz", "paz", "bar", "foo")
	expectZRange(t, conn, "zset_key", -100, 1, "baz", "caz")
	expectZRange(t, conn, "zset_key", -100, -4, "baz", "caz")
	expectZRange(t, conn, "zset_key", -6, 0, "baz")
	do(t, conn, "ZRANGE", "zset_key", "-9223372036854775808", "-1")
	expectReply(t, conn, bulkArray("baz", "caz", "paz", "bar", "foo"))
	do(t, conn, "ZRANGE", "zset_key", "-9223372036854775808", "9223372036854775807")
	expectReply(t, conn, bulkArray("baz", "caz", "paz", "bar", "foo"))
}

func TestZRangeEmptyRangesWithNegativeIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	for _, indexes := range [][2]string{
		// The stop is before the first member.
		{"0", "-6"}, {"0", "-100"}, {"-100", "-6"}, {"-9223372036854775808", "-9223372036854775808"},
		// The start is after the stop.
		{"-1", "-2"}, {"-1", "0"}, {"-2", "-5"}, {"3", "-3"}, {"-1", "3"},
		// The start is after the last member.
		{"5", "-1"}, {"100", "-1"},
	} {
		do(t, conn, "ZRANGE", "zset_key", indexes[0], indexes[1])
		expectReply(t, conn, "*0\r\n")
	}
}

func TestZRangeOfAMissingKeyWithNegativeIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, indexes := range [][2]string{{"0", "-1"}, {"-2", "-1"}, {"-100", "100"}} {
		do(t, conn, "ZRANGE", "missing_key", indexes[0], indexes[1])
		expectReply(t, conn, "*0\r\n")
	}
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
}

func TestZRangeOfASingleMemberWithNegativeIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "only")
	expectZRange(t, conn, "zset_key", 0, -1, "only")
	expectZRange(t, conn, "zset_key", -1, -1, "only")
	expectZRange(t, conn, "zset_key", -1, 0, "only")
	expectZRange(t, conn, "zset_key", -5, 5, "only")
	expectZRange(t, conn, "zset_key", 0, -2)
	expectZRange(t, conn, "zset_key", 1, -1)
}

// TestZRangeMatchesModel compares ZRANGE with a deliberately naive
// implementation of its rules, for every combination of small indexes and
// for sets of every small size.
func TestZRangeMatchesModel(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// model resolves each index to a position, then collects the members one
	// by one.
	model := func(members []string, start, stop int) []string {
		n := len(members)
		if start < 0 {
			start += n
		}
		if stop < 0 {
			stop += n
		}
		var out []string
		for i := range members {
			if i >= start && i <= stop {
				out = append(out, members[i])
			}
		}
		return out
	}

	for size := 0; size <= 5; size++ {
		key := "zset_" + strconv.Itoa(size)
		members := make([]string, size)
		for i := range members {
			members[i] = "member-" + strconv.Itoa(i)
			zadd(t, conn, key, strconv.Itoa(i), members[i])
		}

		for start := -8; start <= 8; start++ {
			for stop := -8; stop <= 8; stop++ {
				do(t, conn, "ZRANGE", key, strconv.Itoa(start), strconv.Itoa(stop))
				expectReply(t, conn, bulkArray(model(members, start, stop)...))
			}
		}
	}
}

func TestZRangeWithNegativeIndexesFollowsTheChangesOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a", "20", "b", "30", "c")
	expectZRange(t, conn, "zset_key", -2, -1, "b", "c")

	// The last members are the last ones at the time of the command.
	zadd(t, conn, "zset_key", "40", "d")
	expectZRange(t, conn, "zset_key", -2, -1, "c", "d")
	do(t, conn, "ZADD", "zset_key", "100", "a")
	expectReply(t, conn, ":0\r\n")
	expectZRange(t, conn, "zset_key", -2, -1, "d", "a")
	expectZRange(t, conn, "zset_key", 0, -1, "b", "c", "d", "a")
}
