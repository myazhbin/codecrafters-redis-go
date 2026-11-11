package main

import (
	"math/rand"
	"net"
	"sort"
	"strconv"
	"testing"
	"time"
)

// expectZRange checks that ZRANGE key start stop replies with the given
// members, in that order.
func expectZRange(t *testing.T, conn net.Conn, key string, start, stop int, want ...string) {
	t.Helper()

	do(t, conn, "ZRANGE", key, strconv.Itoa(start), strconv.Itoa(stop))
	expectReply(t, conn, bulkArray(want...))
}

// addTestSortedSet creates the sorted set the tests of ZRANGE work with.
// Ordered by score, its members are baz, caz, paz, bar and foo.
func addTestSortedSet(t *testing.T, conn net.Conn) {
	t.Helper()

	zadd(t, conn, "zset_key", "100.0", "foo")
	zadd(t, conn, "zset_key", "100.0", "bar")
	zadd(t, conn, "zset_key", "20.0", "baz")
	zadd(t, conn, "zset_key", "30.1", "caz")
	zadd(t, conn, "zset_key", "40.2", "paz")
}

func TestZRange(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	do(t, conn, "ZRANGE", "zset_key", "2", "4")
	expectReply(t, conn, "*3\r\n$3\r\npaz\r\n$3\r\nbar\r\n$3\r\nfoo\r\n")

	// Both ends of the range are included.
	expectZRange(t, conn, "zset_key", 0, 4, "baz", "caz", "paz", "bar", "foo")
	expectZRange(t, conn, "zset_key", 0, 2, "baz", "caz", "paz")
	expectZRange(t, conn, "zset_key", 1, 3, "caz", "paz", "bar")
	expectZRange(t, conn, "zset_key", 0, 0, "baz")
	expectZRange(t, conn, "zset_key", 4, 4, "foo")
	expectZRange(t, conn, "zset_key", 3, 4, "bar", "foo")
}

func TestZRangeOrdersMembersByScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "racer_scores", "8.1", "Sam-Bodden")
	zadd(t, conn, "racer_scores", "10.2", "Royce")
	zadd(t, conn, "racer_scores", "6.0", "Ford")
	zadd(t, conn, "racer_scores", "14.1", "Prickett")

	expectZRange(t, conn, "racer_scores", 0, 2, "Ford", "Sam-Bodden", "Royce")
	expectZRange(t, conn, "racer_scores", 0, 3, "Ford", "Sam-Bodden", "Royce", "Prickett")
}

func TestZRangeStopBeyondTheEndOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	// The range stops at the last member.
	expectZRange(t, conn, "zset_key", 0, 5, "baz", "caz", "paz", "bar", "foo")
	expectZRange(t, conn, "zset_key", 0, 100, "baz", "caz", "paz", "bar", "foo")
	expectZRange(t, conn, "zset_key", 3, 1000000, "bar", "foo")
	expectZRange(t, conn, "zset_key", 4, 9223372036854775807, "foo")
}

func TestZRangeStartBeyondTheEndOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	// There is nothing from the cardinality of the set onwards.
	for _, indexes := range [][2]int{{5, 5}, {5, 10}, {6, 10}, {100, 200}, {9223372036854775807, 9223372036854775807}} {
		do(t, conn, "ZRANGE", "zset_key", strconv.Itoa(indexes[0]), strconv.Itoa(indexes[1]))
		expectReply(t, conn, "*0\r\n")
	}
}

func TestZRangeStartAfterStop(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	for _, indexes := range [][2]int{{1, 0}, {3, 1}, {4, 0}, {2, 1}, {100, 0}} {
		do(t, conn, "ZRANGE", "zset_key", strconv.Itoa(indexes[0]), strconv.Itoa(indexes[1]))
		expectReply(t, conn, "*0\r\n")
	}
}

func TestZRangeOfAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A set that doesn't exist is like an empty one.
	do(t, conn, "ZRANGE", "missing_key", "0", "2")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "ZRANGE", "missing_key", "0", "0")
	expectReply(t, conn, "*0\r\n")

	// Listing it didn't create it.
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestZRangeOrdersMembersWithTheSameScoreByName(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, member := range []string{"pear", "apple", "Zebra", "apples", "banana", "app"} {
		zadd(t, conn, "zset_key", "7", member)
	}
	zadd(t, conn, "zset_key", "6.9", "zzz")
	zadd(t, conn, "zset_key", "7.1", "aaa")

	expectZRange(t, conn, "zset_key", 0, 7, "zzz", "Zebra", "app", "apple", "apples", "banana", "pear", "aaa")
	expectZRange(t, conn, "zset_key", 2, 4, "app", "apple", "apples")
}

func TestZRangeFollowsTheChangesOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a", "20", "b", "30", "c")
	expectZRange(t, conn, "zset_key", 0, 10, "a", "b", "c")

	zadd(t, conn, "zset_key", "15", "new")
	expectZRange(t, conn, "zset_key", 0, 10, "a", "new", "b", "c")
	expectZRange(t, conn, "zset_key", 1, 2, "new", "b")

	// A member whose score changes changes place.
	do(t, conn, "ZADD", "zset_key", "100", "a")
	expectReply(t, conn, ":0\r\n")
	expectZRange(t, conn, "zset_key", 0, 10, "new", "b", "c", "a")
	expectZRange(t, conn, "zset_key", 0, 0, "new")
}

func TestZRangeOfARandomSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Neither the members nor their order can be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	type scored struct {
		member string
		score  int
	}
	members := make([]scored, 40)
	for i := range members {
		members[i] = scored{randomWord(t), rng.Intn(8)}
		zadd(t, conn, "zset_key", strconv.Itoa(members[i].score), members[i].member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].score != members[j].score {
			return members[i].score < members[j].score
		}
		return members[i].member < members[j].member
	})
	names := make([]string, len(members))
	for i, m := range members {
		names[i] = m.member
	}

	expectZRange(t, conn, "zset_key", 0, len(names)-1, names...)
	for range 20 {
		start := rng.Intn(len(names))
		stop := start + rng.Intn(len(names)-start)
		expectZRange(t, conn, "zset_key", start, stop, names[start:stop+1]...)
	}
}

func TestZRangeOfALargeSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const members = 3000
	want := make([]string, members)
	for i := range want {
		want[i] = "member-" + strconv.Itoa(i)
	}
	for _, i := range rand.New(rand.NewSource(1)).Perm(members) {
		zadd(t, conn, "zset_key", strconv.Itoa(i), want[i])
	}

	expectZRange(t, conn, "zset_key", 0, members-1, want...)
	expectZRange(t, conn, "zset_key", 1000, 1009, want[1000:1010]...)
	expectZRange(t, conn, "zset_key", members-2, members+5, want[members-2:]...)
}

func TestZRangeMembersAreBinarySafe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	members := []string{"", "two words", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n"}
	for i, member := range members {
		zadd(t, conn, "zset_key", strconv.Itoa(i), member)
	}
	expectZRange(t, conn, "zset_key", 0, 5, members...)
}

func TestZRangeInvalidIndexes(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	for _, indexes := range [][2]string{
		{"a", "1"}, {"0", "b"}, {"", "1"}, {"0", ""}, {"1.5", "2"}, {"0", "2.0"}, {" 0", "1"}, {"0", "1 "},
		{"0x1", "2"}, {"+0", "1"}, {"9223372036854775808", "1"}, {"0", "-9223372036854775809"},
	} {
		do(t, conn, "ZRANGE", "zset_key", indexes[0], indexes[1])
		expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
	}

	// The indexes are checked whether the key exists or not.
	do(t, conn, "ZRANGE", "missing_key", "a", "1")
	expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
}

func TestZRangeWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addTestSortedSet(t, conn)

	for _, command := range [][]string{{"ZRANGE"}, {"ZRANGE", "zset_key"}, {"ZRANGE", "zset_key", "0"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zrange' command\r\n")
	}
	do(t, conn, "ZRANGE", "zset_key", "0", "1", "NOSUCHOPTION")
	expectReply(t, conn, "-ERR syntax error\r\n")
}

func TestZRangeOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZRANGE", key, "0", "1")
		expectReply(t, conn, wrongTypeError)
	}
	// LRANGE lists a list, not a sorted set.
	zadd(t, conn, "zset_key", "1", "a")
	do(t, conn, "LRANGE", "zset_key", "0", "1")
	expectReply(t, conn, wrongTypeError)
}

func TestZRangeCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	for _, name := range []string{"ZRANGE", "zrange", "ZRange"} {
		do(t, conn, name, "zset_key", "0", "1")
		expectReply(t, conn, bulkArray("a", "b"))
	}
}

func TestZRangeInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZRANGE", "zset_key", "0", "5"},
		{"ZADD", "zset_key", "5", "b"},
		{"ZRANGE", "zset_key", "0", "5"},
		{"ZRANGE", "zset_key", "0", "x"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(bulkArray("a"), ":1\r\n", bulkArray("b", "a"), "-ERR value is not an integer or out of range\r\n"))
}

func TestZRangeIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a")
	expectPropagated(t, replica, "ZADD", "zset_key", "1", "a")
	do(t, watcher, "WATCH", "zset_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectZRange(t, conn, "zset_key", 0, 5, "a")
	expectZRange(t, conn, "missing_key", 0, 5)

	// Listing members is neither logged nor propagated, doesn't count as a
	// modification of the key, and leaves the set as it is.
	if got, want := readFile(t, incrPath), encodeCommand("ZADD", "zset_key", "1", "a"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)
	do(t, watcher, "MULTI")
	expectReply(t, watcher, "+OK\r\n")
	do(t, watcher, "PING")
	expectReply(t, watcher, "+QUEUED\r\n")
	do(t, watcher, "EXEC")
	expectReply(t, watcher, array("+PONG\r\n"))
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1})
}
