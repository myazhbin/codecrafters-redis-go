package main

import (
	"math/rand"
	"net"
	"sort"
	"strconv"
	"testing"
	"time"
)

// zadd adds members to a sorted set, each given as its score followed by its
// name, and checks that all of them are new to the set.
func zadd(t *testing.T, conn net.Conn, key string, scoresAndMembers ...string) {
	t.Helper()

	do(t, conn, append([]string{"ZADD", key}, scoresAndMembers...)...)
	expectReply(t, conn, ":"+strconv.Itoa(len(scoresAndMembers)/2)+"\r\n")
}

func TestZRank(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "100.0", "foo")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "100.0", "bar")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "20.0", "baz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "30.1", "caz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "40.2", "paz")
	expectReply(t, conn, ":1\r\n")

	// The rank is the position by increasing score, counting from 0.
	do(t, conn, "ZRANK", "zset_key", "caz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZRANK", "zset_key", "baz")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZRANK", "zset_key", "foo")
	expectReply(t, conn, ":4\r\n")
	do(t, conn, "ZRANK", "zset_key", "bar")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "ZRANK", "zset_key", "paz")
	expectReply(t, conn, ":2\r\n")

	// There is no rank for a member that isn't in the set, or for a set that
	// doesn't exist.
	do(t, conn, "ZRANK", "zset_key", "missing_member")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "ZRANK", "missing_key", "member")
	expectReply(t, conn, "$-1\r\n")
}

func TestZRankOfMembersWithTheSameScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1.0", "member_with_score_1")
	zadd(t, conn, "zset_key", "2.0", "member_with_score_2")
	zadd(t, conn, "zset_key", "2.0", "another_member_with_score_2")

	do(t, conn, "ZRANK", "zset_key", "member_with_score_1")
	expectReply(t, conn, ":0\r\n")
	// Both members have the same score: the one whose name comes first
	// lexicographically has the lower rank.
	do(t, conn, "ZRANK", "zset_key", "member_with_score_2")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "ZRANK", "zset_key", "another_member_with_score_2")
	expectReply(t, conn, ":1\r\n")
}

func TestZRankOrdersNamesByTheirBytes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Upper case comes before lower case, digits before letters, and a name
	// after the names it starts with.
	inOrder := []string{"", "10", "9", "Zebra", "app", "apple", "apples", "banana", "pear", "\xff"}
	for _, i := range rand.New(rand.NewSource(1)).Perm(len(inOrder)) {
		zadd(t, conn, "zset_key", "7", inOrder[i])
	}
	for rank, member := range inOrder {
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, ":"+strconv.Itoa(rank)+"\r\n")
	}
}

func TestZRankWithNegativeAndInfiniteScores(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "0", "zero", "-1.5", "negative", "inf", "highest", "-inf", "lowest", "1e300", "huge", "0.001", "tiny")
	for rank, member := range []string{"lowest", "negative", "zero", "tiny", "huge", "highest"} {
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, ":"+strconv.Itoa(rank)+"\r\n")
	}
}

func TestZRankFollowsTheChangesOfTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))
	rank := func(member, want string) {
		t.Helper()
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, want)
	}

	zadd(t, conn, "zset_key", "10", "a", "20", "b", "30", "c")
	rank("a", ":0\r\n")
	rank("b", ":1\r\n")
	rank("c", ":2\r\n")

	// A member that comes before the others pushes them back by one.
	zadd(t, conn, "zset_key", "5", "first")
	rank("first", ":0\r\n")
	rank("a", ":1\r\n")
	rank("c", ":3\r\n")
	// One that comes after changes nothing for them.
	zadd(t, conn, "zset_key", "50", "last")
	rank("c", ":3\r\n")
	rank("last", ":4\r\n")

	// A member whose score changes gets the rank of its new score.
	do(t, conn, "ZADD", "zset_key", "25", "a")
	expectReply(t, conn, ":0\r\n")
	rank("first", ":0\r\n")
	rank("b", ":1\r\n")
	rank("a", ":2\r\n")
	rank("c", ":3\r\n")
	rank("last", ":4\r\n")
}

func TestZRankOfRandomMembers(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Neither the names nor the ranks can be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	type scored struct {
		member string
		score  int
	}
	members := make([]scored, 50)
	for i := range members {
		members[i] = scored{randomWord(t), rng.Intn(10)}
		zadd(t, conn, "zset_key", strconv.Itoa(members[i].score), members[i].member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].score != members[j].score {
			return members[i].score < members[j].score
		}
		return members[i].member < members[j].member
	})

	for rank, m := range members {
		do(t, conn, "ZRANK", "zset_key", m.member)
		expectReply(t, conn, ":"+strconv.Itoa(rank)+"\r\n")
	}
}

func TestZRankOfAMissingMemberOrKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "member")

	// Names are exact: nothing is trimmed, and the case matters.
	for _, member := range []string{"missing_member", "Member", "member ", "memb", "members", "", "1", "*"} {
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, "$-1\r\n")
	}
	for _, key := range []string{"missing_key", "ZSET_KEY", "zset_key ", "member", ""} {
		do(t, conn, "ZRANK", key, "member")
		expectReply(t, conn, "$-1\r\n")
	}

	// Asking for a rank creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("zset_key"))
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
}

func TestZRankMembersAreBinarySafe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	members := []string{"", "two words", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n"}
	for i, member := range members {
		zadd(t, conn, "zset_key", strconv.Itoa(i), member)
	}
	for rank, member := range members {
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, ":"+strconv.Itoa(rank)+"\r\n")
	}
}

func TestZRankOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "member", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZRANK", key, "member")
		expectReply(t, conn, wrongTypeError)
	}
}

func TestZRankWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ZRANK"}, {"ZRANK", "zset_key"}, {"ZRANK", "zset_key", "a", "b"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zrank' command\r\n")
	}
}

func TestZRankCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	for _, name := range []string{"ZRANK", "zrank", "ZRank"} {
		do(t, conn, name, "zset_key", "b")
		expectReply(t, conn, ":1\r\n")
	}
}

func TestZRankInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZRANK", "zset_key", "a"},
		{"ZADD", "zset_key", "5", "b"},
		{"ZRANK", "zset_key", "a"},
		{"ZRANK", "zset_key", "missing"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Each command sees the set as the previous ones left it.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":0\r\n", ":1\r\n", ":1\r\n", "$-1\r\n"))
}

func TestZRankIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a")
	expectPropagated(t, replica, "ZADD", "zset_key", "1", "a")
	do(t, watcher, "WATCH", "zset_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	do(t, conn, "ZRANK", "zset_key", "a")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZRANK", "missing_key", "a")
	expectReply(t, conn, "$-1\r\n")

	// Reading a rank is neither logged nor propagated, and doesn't count as
	// a modification of the key.
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
}
