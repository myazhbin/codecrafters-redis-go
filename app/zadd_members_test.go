package main

import (
	"math"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// expectSortedSet checks that the sorted set stored at key on srv holds
// exactly the given members, in that order.
func expectSortedSet(t *testing.T, srv *Server, key string, want ...scoredMember) {
	t.Helper()

	if got := sortedSetMembers(t, srv, key); !reflect.DeepEqual(got, want) {
		t.Errorf("sorted set %q holds %v, want %v", key, got, want)
	}
}

func TestZAddAddsMembersToAnExistingSortedSet(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "20.0", "zset_member1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "30.1", "zset_member2")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "40.2", "zset_member3")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "50.3", "zset_member4")
	expectReply(t, conn, ":1\r\n")
	expectSortedSet(t, srv, "zset_key",
		scoredMember{"zset_member1", 20}, scoredMember{"zset_member2", 30.1},
		scoredMember{"zset_member3", 40.2}, scoredMember{"zset_member4", 50.3})

	// The member is already in the set: its score is updated, and no member
	// is added.
	do(t, conn, "ZADD", "zset_key", "100.0", "zset_member1")
	expectReply(t, conn, ":0\r\n")
	expectSortedSet(t, srv, "zset_key",
		scoredMember{"zset_member2", 30.1}, scoredMember{"zset_member3", 40.2},
		scoredMember{"zset_member4", 50.3}, scoredMember{"zset_member1", 100})
}

func TestZAddUpdatesTheScoreOfAnExistingMember(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "0.0043", "foo")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "8.0", "bar")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "10.0", "bar")
	expectReply(t, conn, ":0\r\n")

	// The member is in the set once, with its last score.
	expectSortedSet(t, srv, "zset_key", scoredMember{"foo", 0.0043}, scoredMember{"bar", 10})
}

func TestZAddKeepsMembersInOrder(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Whatever the order they are added in, members are kept by increasing
	// score.
	for _, m := range []scoredMember{{"d", 4}, {"a", 1}, {"e", 5.5}, {"c", 3}, {"b", 1.5}, {"z", -2}, {"y", -0.5}, {"zero", 0}} {
		do(t, conn, "ZADD", "zset_key", strconv.FormatFloat(m.score, 'f', -1, 64), m.member)
		expectReply(t, conn, ":1\r\n")
	}
	expectSortedSet(t, srv, "zset_key",
		scoredMember{"z", -2}, scoredMember{"y", -0.5}, scoredMember{"zero", 0}, scoredMember{"a", 1},
		scoredMember{"b", 1.5}, scoredMember{"c", 3}, scoredMember{"d", 4}, scoredMember{"e", 5.5})
}

func TestZAddOrdersMembersWithTheSameScoreByName(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The order is the one of the bytes: upper case comes before lower case,
	// and a name comes after the names it starts with.
	for _, member := range []string{"pear", "apple", "Zebra", "apples", "banana", "", "app", "\xff", "10", "9"} {
		do(t, conn, "ZADD", "zset_key", "7", member)
		expectReply(t, conn, ":1\r\n")
	}
	do(t, conn, "ZADD", "zset_key", "6.9", "zzz")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "7.1", "aaa")
	expectReply(t, conn, ":1\r\n")

	expectSortedSet(t, srv, "zset_key",
		scoredMember{"zzz", 6.9},
		scoredMember{"", 7}, scoredMember{"10", 7}, scoredMember{"9", 7}, scoredMember{"Zebra", 7},
		scoredMember{"app", 7}, scoredMember{"apple", 7}, scoredMember{"apples", 7},
		scoredMember{"banana", 7}, scoredMember{"pear", 7}, scoredMember{"\xff", 7},
		scoredMember{"aaa", 7.1})
}

func TestZAddMovesAMemberWhoseScoreChanges(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	for i, member := range []string{"a", "b", "c", "d"} {
		do(t, conn, "ZADD", "zset_key", strconv.Itoa(i+1), member)
		expectReply(t, conn, ":1\r\n")
	}

	steps := []struct {
		score string
		want  []scoredMember
	}{
		// To the end, to the start and to the middle.
		{"10", []scoredMember{{"a", 1}, {"c", 3}, {"d", 4}, {"b", 10}}},
		{"-10", []scoredMember{{"b", -10}, {"a", 1}, {"c", 3}, {"d", 4}}},
		{"3.5", []scoredMember{{"a", 1}, {"c", 3}, {"b", 3.5}, {"d", 4}}},
		// Next to a member that has the same score, on the side its name
		// puts it.
		{"3", []scoredMember{{"a", 1}, {"b", 3}, {"c", 3}, {"d", 4}}},
		{"4", []scoredMember{{"a", 1}, {"c", 3}, {"b", 4}, {"d", 4}}},
		// As far as a score goes.
		{"inf", []scoredMember{{"a", 1}, {"c", 3}, {"d", 4}, {"b", math.Inf(1)}}},
		{"-inf", []scoredMember{{"b", math.Inf(-1)}, {"a", 1}, {"c", 3}, {"d", 4}}},
		// And back to where it was.
		{"2", []scoredMember{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}}},
	}
	for _, step := range steps {
		do(t, conn, "ZADD", "zset_key", step.score, "b")
		expectReply(t, conn, ":0\r\n")
		expectSortedSet(t, srv, "zset_key", step.want...)
	}
}

func TestZAddSeveralMembersAtOnce(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "3", "c", "1", "a", "2", "b")
	expectReply(t, conn, ":3\r\n")
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1}, scoredMember{"b", 2}, scoredMember{"c", 3})

	// Only the members that are new count.
	do(t, conn, "ZADD", "zset_key", "5", "a", "4", "d", "2", "b")
	expectReply(t, conn, ":1\r\n")
	expectSortedSet(t, srv, "zset_key", scoredMember{"b", 2}, scoredMember{"c", 3}, scoredMember{"d", 4}, scoredMember{"a", 5})

	// A member that is given twice is added once, with its last score.
	do(t, conn, "ZADD", "zset_key", "9", "e", "0", "e")
	expectReply(t, conn, ":1\r\n")
	expectSortedSet(t, srv, "zset_key",
		scoredMember{"e", 0}, scoredMember{"b", 2}, scoredMember{"c", 3}, scoredMember{"d", 4}, scoredMember{"a", 5})
}

func TestZAddIsAppliedInFullOrNotAtAll(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "1", "a")
	expectReply(t, conn, ":1\r\n")

	// One invalid score, wherever it is, and nothing is added or updated.
	do(t, conn, "ZADD", "zset_key", "2", "b", "nope", "c", "5", "a")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")
	do(t, conn, "ZADD", "zset_key", "5", "a", "3", "c", "nan", "d")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")
	do(t, conn, "ZADD", "zset_key", "5", "a", "3")
	expectReply(t, conn, "-ERR syntax error\r\n")

	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1})
}

func TestZAddWithTheScoreAMemberAlreadyHas(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	do(t, conn, "ZADD", "zset_key", "1.5", "a")
	expectReply(t, conn, ":1\r\n")
	expectPropagated(t, replica, "ZADD", "zset_key", "1.5", "a")

	do(t, watcher, "WATCH", "zset_key")
	expectReply(t, watcher, "+OK\r\n")

	// The set is left exactly as it is, however the score is written.
	for _, score := range []string{"1.5", "1.50", "15e-1", "+1.5"} {
		do(t, conn, "ZADD", "zset_key", score, "a")
		expectReply(t, conn, ":0\r\n")
	}
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1.5})

	// That isn't a write: nothing is logged or propagated, and the
	// transaction of a client that watches the key goes through.
	if got, want := readFile(t, incrPath), encodeCommand("ZADD", "zset_key", "1.5", "a"); got != want {
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

func TestZAddScoreUpdateIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	commands := [][]string{
		{"ZADD", "zset_key", "1", "a", "2", "b"},
		{"ZADD", "zset_key", "3", "c"},
		{"ZADD", "zset_key", "10", "a"},
	}
	want := ""
	for i, command := range commands {
		if i == len(commands)-1 {
			do(t, watcher, "WATCH", "zset_key")
			expectReply(t, watcher, "+OK\r\n")
		}
		do(t, conn, command...)
		drainPendingReply(t, conn)
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}
	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}

	// Changing a score, with no member added, modifies the key.
	do(t, watcher, "MULTI")
	expectReply(t, watcher, "+OK\r\n")
	do(t, watcher, "PING")
	expectReply(t, watcher, "+QUEUED\r\n")
	do(t, watcher, "EXEC")
	expectReply(t, watcher, "*-1\r\n")

	// A server that restarts rebuilds the same set.
	restarted := restartWithAOF(t, srv, args...)
	expectSortedSet(t, restarted, "zset_key", scoredMember{"b", 2}, scoredMember{"c", 3}, scoredMember{"a", 10})
}

func TestZAddZeroAndNegativeZeroAreTheSameScore(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "0", "b", "-0", "a", "0", "c")
	expectReply(t, conn, ":3\r\n")
	// The three members are ordered by name, as their scores are equal.
	members := sortedSetMembers(t, srv, "zset_key")
	var names []string
	for _, m := range members {
		names = append(names, m.member)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(names, want) {
		t.Errorf("members are in the order %q, want %q", names, want)
	}
}

func TestSortedSetsAreIndependent(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "first", "1", "shared", "2", "only-in-first")
	expectReply(t, conn, ":2\r\n")
	// The member is new to the other set, whatever the first one holds.
	do(t, conn, "ZADD", "second", "50", "shared")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "first", "3", "shared")
	expectReply(t, conn, ":0\r\n")

	expectSortedSet(t, srv, "first", scoredMember{"only-in-first", 2}, scoredMember{"shared", 3})
	expectSortedSet(t, srv, "second", scoredMember{"shared", 50})
}

func TestZAddManyMembers(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Members are added in a random order, with scores that repeat.
	const members = 2000
	rng := rand.New(rand.NewSource(1))
	want := make(map[string]float64, members)
	for _, i := range rng.Perm(members) {
		member, score := "member-"+strconv.Itoa(i), float64(i%50)
		do(t, conn, "ZADD", "zset_key", strconv.FormatFloat(score, 'f', -1, 64), member)
		expectReply(t, conn, ":1\r\n")
		want[member] = score
	}
	// Then half of them change score.
	for _, i := range rng.Perm(members)[:members/2] {
		member, score := "member-"+strconv.Itoa(i), float64(rng.Intn(100))-50.5
		do(t, conn, "ZADD", "zset_key", strconv.FormatFloat(score, 'f', -1, 64), member)
		expectReply(t, conn, ":0\r\n")
		want[member] = score
	}

	// sortedSetMembers checks that the set is in order.
	got := sortedSetMembers(t, srv, "zset_key")
	if len(got) != members {
		t.Fatalf("sorted set has %d members, want %d", len(got), members)
	}
	for _, m := range got {
		if want[m.member] != m.score {
			t.Fatalf("member %q has score %v, want %v", m.member, m.score, want[m.member])
		}
	}
}

func TestZAddSeveralTimesInATransaction(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "zset_key", "1", "a")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZADD", "zset_key", "2", "b"},
		{"ZADD", "zset_key", "5", "a"},
		{"ZADD", "zset_key", "3", "c", "4", "d", "2", "b"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	expectSortedSet(t, srv, "zset_key", scoredMember{"a", 1})

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", ":0\r\n", ":2\r\n"))
	expectSortedSet(t, srv, "zset_key", scoredMember{"b", 2}, scoredMember{"c", 3}, scoredMember{"d", 4}, scoredMember{"a", 5})
}
