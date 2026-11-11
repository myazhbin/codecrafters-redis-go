package main

import (
	"math/rand"
	"strconv"
	"testing"
	"time"
)

func TestZRem(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "80.5", "foo")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "50.3", "baz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "80.5", "bar")
	expectReply(t, conn, ":1\r\n")

	// The reply is the number of members removed, as a RESP integer.
	do(t, conn, "ZREM", "zset_key", "baz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZRANGE", "zset_key", "0", "-1")
	expectReply(t, conn, "*2\r\n$3\r\nbar\r\n$3\r\nfoo\r\n")

	// A member that isn't in the set can't be removed.
	do(t, conn, "ZREM", "zset_key", "missing_member")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZRANGE", "zset_key", "0", "-1")
	expectReply(t, conn, bulkArray("bar", "foo"))
}

func TestZRemRemovesTheMemberThatIsNamed(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "racer_scores", "8.3", "Sam-Bodden")
	zadd(t, conn, "racer_scores", "10.5", "Royce")

	do(t, conn, "ZREM", "racer_scores", "Royce")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "racer_scores", 0, -1, "Sam-Bodden")
	do(t, conn, "ZREM", "racer_scores", "missing_member")
	expectReply(t, conn, ":0\r\n")
	// It is gone: removing it again removes nothing.
	do(t, conn, "ZREM", "racer_scores", "Royce")
	expectReply(t, conn, ":0\r\n")
	expectZRange(t, conn, "racer_scores", 0, -1, "Sam-Bodden")
}

func TestRemovedMemberIsGoneForEveryCommand(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "10", "a", "20", "b", "30", "c", "40", "d")
	do(t, conn, "ZREM", "zset_key", "b")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "ZSCORE", "zset_key", "b")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "ZRANK", "zset_key", "b")
	expectReply(t, conn, "$-1\r\n")
	expectZCard(t, conn, "zset_key", 3)
	expectZRange(t, conn, "zset_key", 0, -1, "a", "c", "d")

	// The members that came after it move up by one rank; the others keep
	// theirs. All of them keep their score.
	for rank, member := range []string{"a", "c", "d"} {
		do(t, conn, "ZRANK", "zset_key", member)
		expectReply(t, conn, ":"+strconv.Itoa(rank)+"\r\n")
	}
	expectZScore(t, conn, "zset_key", "c", "30")
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 10}, scoredMember{"c", 30}, scoredMember{"d", 40})
}

func TestZRemTheFirstAndTheLastMember(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "10", "a", "20", "b", "30", "c", "40", "d")
	do(t, conn, "ZREM", "zset_key", "a")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "zset_key", 0, -1, "b", "c", "d")
	do(t, conn, "ZREM", "zset_key", "d")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "zset_key", 0, -1, "b", "c")
	expectSortedSet(t, srv, "zset_key", scoredMember{"b", 20}, scoredMember{"c", 30})
}

func TestZRemAmongMembersWithTheSameScore(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "7", "pear", "7", "apple", "7", "banana", "7", "cherry")
	// The member is found by its name, among others that have its score.
	do(t, conn, "ZREM", "zset_key", "banana")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "zset_key", 0, -1, "apple", "cherry", "pear")
	do(t, conn, "ZREM", "zset_key", "apple")
	expectReply(t, conn, ":1\r\n")
	expectSortedSet(t, srv, "zset_key", scoredMember{"cherry", 7}, scoredMember{"pear", 7})
}

func TestZRemOfTheLastMemberDeletesTheKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	do(t, conn, "ZREM", "zset_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+zset\r\n")

	// An empty sorted set doesn't exist.
	do(t, conn, "ZREM", "zset_key", "b")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
	expectZCard(t, conn, "zset_key", 0)
	expectZRange(t, conn, "zset_key", 0, -1)

	// The key is free for a value of any type.
	do(t, conn, "RPUSH", "zset_key", "element")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+list\r\n")
}

func TestZRemSeveralMembersAtOnce(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "1", "a", "2", "b", "3", "c", "4", "d", "5", "e")

	// Only the members that were in the set count, and each of them once.
	do(t, conn, "ZREM", "zset_key", "b", "missing", "d", "b")
	expectReply(t, conn, ":2\r\n")
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1}, scoredMember{"c", 3}, scoredMember{"e", 5})

	do(t, conn, "ZREM", "zset_key", "missing", "other")
	expectReply(t, conn, ":0\r\n")
	// All that is left, which deletes the key.
	do(t, conn, "ZREM", "zset_key", "e", "a", "c", "zzz")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+none\r\n")
}

func TestZRemOnAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZREM", "missing_key", "member")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZREM", "missing_key", "a", "b", "c")
	expectReply(t, conn, ":0\r\n")

	// Nothing was created.
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestZRemNamesAreExact(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "member")
	for _, member := range []string{"Member", "MEMBER", "member ", " member", "memb", "members", "", "1", "*", "m*"} {
		do(t, conn, "ZREM", "zset_key", member)
		expectReply(t, conn, ":0\r\n")
	}
	for _, key := range []string{"ZSET_KEY", "zset_key ", "member"} {
		do(t, conn, "ZREM", key, "member")
		expectReply(t, conn, ":0\r\n")
	}
	expectZRange(t, conn, "zset_key", 0, -1, "member")
}

func TestZRemMembersAreBinarySafe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	members := []string{"", "two words", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n"}
	for i, member := range members {
		zadd(t, conn, "zset_key", strconv.Itoa(i), member)
	}
	for i, member := range members {
		do(t, conn, "ZREM", "zset_key", member)
		expectReply(t, conn, ":1\r\n")
		expectZCard(t, conn, "zset_key", len(members)-i-1)
	}
}

func TestMemberCanBeAddedAgainAfterItWasRemoved(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a", "20", "b")
	do(t, conn, "ZREM", "zset_key", "a")
	expectReply(t, conn, ":1\r\n")

	// It is a new member again, with the score it is given now.
	do(t, conn, "ZADD", "zset_key", "30.5", "a")
	expectReply(t, conn, ":1\r\n")
	expectZScore(t, conn, "zset_key", "a", "30.5")
	expectZRange(t, conn, "zset_key", 0, -1, "b", "a")
}

func TestZRemManyMembers(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	const members = 1500
	rng := rand.New(rand.NewSource(1))
	left := make(map[string]float64, members)
	for _, i := range rng.Perm(members) {
		member, score := "member-"+strconv.Itoa(i), float64(i%40)
		zadd(t, conn, "zset_key", strconv.FormatFloat(score, 'f', -1, 64), member)
		left[member] = score
	}
	// Two thirds of them are removed, in a random order.
	for _, i := range rng.Perm(members)[:2*members/3] {
		member := "member-" + strconv.Itoa(i)
		do(t, conn, "ZREM", "zset_key", member)
		expectReply(t, conn, ":1\r\n")
		delete(left, member)
	}

	// sortedSetMembers checks that the set is still in order.
	got := sortedSetMembers(t, srv, "zset_key")
	if len(got) != len(left) {
		t.Fatalf("sorted set has %d members, want %d", len(got), len(left))
	}
	for _, m := range got {
		if score, ok := left[m.member]; !ok || score != m.score {
			t.Fatalf("member %q is in the set with score %v; it should have score %v (still in the set: %t)", m.member, m.score, score, ok)
		}
	}
	expectZCard(t, conn, "zset_key", len(left))
}

func TestZRemOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "member")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "member", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZREM", key, "member")
		expectReply(t, conn, wrongTypeError)
	}
	do(t, conn, "GET", "string_key")
	expectReply(t, conn, bulk("member"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("member"))
}

func TestZRemWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ZREM"}, {"ZREM", "zset_key"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zrem' command\r\n")
	}
}

func TestZRemCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "a", "2", "b", "3", "c", "4", "d")
	for i, name := range []string{"ZREM", "zrem", "ZRem"} {
		do(t, conn, name, "zset_key", []string{"a", "b", "c"}[i])
		expectReply(t, conn, ":1\r\n")
	}
	expectZRange(t, conn, "zset_key", 0, -1, "d")
}

func TestZRemInATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a", "2", "b")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZREM", "zset_key", "a"},
		{"ZCARD", "zset_key"},
		{"ZREM", "zset_key", "a", "b"},
		{"TYPE", "zset_key"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Nothing is removed before the transaction is executed.
	expectZCard(t, other, "zset_key", 2)

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", ":1\r\n", ":1\r\n", "+none\r\n"))
}

func TestZRemIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a", "2", "b", "3", "c")
	expectPropagated(t, replica, "ZADD", "zset_key", "1", "a", "2", "b", "3", "c")
	want := encodeCommand("ZADD", "zset_key", "1", "a", "2", "b", "3", "c")

	// A command that removes nothing isn't logged or propagated.
	do(t, conn, "ZREM", "zset_key", "missing")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZREM", "missing_key", "a")
	expectReply(t, conn, ":0\r\n")
	// One that removes something is, as it was received.
	do(t, conn, "ZREM", "zset_key", "b", "missing")
	expectReply(t, conn, ":1\r\n")
	expectPropagated(t, replica, "ZREM", "zset_key", "b", "missing")
	want += encodeCommand("ZREM", "zset_key", "b", "missing")

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)

	// A server that restarts rebuilds the set without the member.
	restarted := restartWithAOF(t, srv, args...)
	expectSortedSet(t, restarted, "zset_key", scoredMember{"a", 1}, scoredMember{"c", 3})
}

func TestZRemIsAppliedByReplicas(t *testing.T) {
	master := startFakeMaster(t)
	srv, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"ZADD", "zset_key", "1", "a", "2", "b"}, []string{"ZREM", "zset_key", "a"})

	conn := dial(t, addr)
	expectEventually(t, conn, ":1\r\n", "ZCARD", "zset_key")
	expectSortedSet(t, srv, "zset_key", scoredMember{"b", 2})
}

func TestZRemModifiesAWatchedKeyOnlyWhenItRemovesSomething(t *testing.T) {
	addr := startTestServer(t)
	conn, watcher := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	transaction := func(want string) {
		t.Helper()
		do(t, watcher, "MULTI")
		expectReply(t, watcher, "+OK\r\n")
		do(t, watcher, "PING")
		expectReply(t, watcher, "+QUEUED\r\n")
		do(t, watcher, "EXEC")
		expectReply(t, watcher, want)
	}

	// Trying to remove a member that isn't there changes nothing.
	do(t, watcher, "WATCH", "zset_key")
	expectReply(t, watcher, "+OK\r\n")
	do(t, conn, "ZREM", "zset_key", "missing")
	expectReply(t, conn, ":0\r\n")
	transaction(array("+PONG\r\n"))

	// Removing a member does, and so does removing the last one, which
	// deletes the key.
	for _, member := range []string{"a", "b"} {
		do(t, watcher, "WATCH", "zset_key")
		expectReply(t, watcher, "+OK\r\n")
		do(t, conn, "ZREM", "zset_key", member)
		expectReply(t, conn, ":1\r\n")
		transaction("*-1\r\n")
	}
}
