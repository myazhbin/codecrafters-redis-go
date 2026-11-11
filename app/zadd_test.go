package main

import (
	"math"
	"reflect"
	"testing"
	"time"
)

// sortedSetMembers returns a copy of the members of the sorted set stored at
// key, in order, read straight from the server's keyspace. It is nil if the
// key doesn't exist. Along the way, it checks that the set is sound: that
// its members are in order, and that it knows the score of each of them.
func sortedSetMembers(t *testing.T, srv *Server, key string) []scoredMember {
	t.Helper()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	z, err := srv.store.getSortedSet(key)
	if err != nil {
		t.Fatalf("reading sorted set %q: %v", key, err)
	}
	if z == nil {
		return nil
	}
	if len(z.ordered) == 0 || len(z.ordered) != len(z.scores) {
		t.Fatalf("sorted set %q has %d members in order and %d scores, want as many of both and at least one", key, len(z.ordered), len(z.scores))
	}
	for i, m := range z.ordered {
		if score, ok := z.scores[m.member]; !ok || score != m.score {
			t.Fatalf("sorted set %q: member %q is at position %d with score %v, but its recorded score is %v (known: %t)", key, m.member, i, m.score, score, ok)
		}
		if i > 0 && compareScoredMembers(z.ordered[i-1], m) >= 0 {
			t.Fatalf("sorted set %q: members %v and %v are out of order", key, z.ordered[i-1], m)
		}
	}
	return append([]scoredMember(nil), z.ordered...)
}

func TestZAddCreatesASortedSet(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// The reply is the number of members added, as a RESP integer.
	do(t, conn, "ZADD", "zset_key", "10.0", "zset_member")
	expectReply(t, conn, ":1\r\n")

	want := []scoredMember{{"zset_member", 10}}
	if got := sortedSetMembers(t, srv, "zset_key"); !reflect.DeepEqual(got, want) {
		t.Errorf("sorted set holds %v, want %v", got, want)
	}
}

func TestZAddCreatesIndependentSortedSets(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Every key gets its own sorted set: each of these commands creates one.
	keys := make([]string, 5)
	for i := range keys {
		keys[i] = randomWord(t)
		do(t, conn, "ZADD", keys[i], "8.5", "member-of-"+keys[i])
		expectReply(t, conn, ":1\r\n")
	}
	for _, key := range keys {
		want := []scoredMember{{"member-of-" + key, 8.5}}
		if got := sortedSetMembers(t, srv, key); !reflect.DeepEqual(got, want) {
			t.Errorf("sorted set %q holds %v, want %v", key, got, want)
		}
	}
}

func TestTypeOfSortedSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "10.0", "zset_member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+zset\r\n")

	// A sorted set is a key like any other.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("zset_key"))
	do(t, conn, "TYPE", "zset_member")
	expectReply(t, conn, "+none\r\n")
}

func TestZAddScoreFormats(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	tests := []struct {
		raw  string
		want float64
	}{
		{"10", 10},
		{"10.0", 10},
		{"8.2", 8.2},
		{"-3.5", -3.5},
		{"+3.5", 3.5},
		{"0", 0},
		{".5", 0.5},
		{"5.", 5},
		{"1e3", 1000},
		{"1.5E-3", 0.0015},
		{"9007199254740993", 9007199254740992}, // more digits than a float64 holds
		{"1.7976931348623157e308", math.MaxFloat64},
		{"inf", math.Inf(1)},
		{"+inf", math.Inf(1)},
		{"-inf", math.Inf(-1)},
		{"Infinity", math.Inf(1)},
		{"-INF", math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			key := "zset_" + tt.raw
			do(t, conn, "ZADD", key, tt.raw, "member")
			expectReply(t, conn, ":1\r\n")

			want := []scoredMember{{"member", tt.want}}
			if got := sortedSetMembers(t, srv, key); !reflect.DeepEqual(got, want) {
				t.Errorf("sorted set holds %v, want %v", got, want)
			}
		})
	}
}

func TestZAddInvalidScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, score := range []string{"abc", "", "nan", "NaN", "-nan", "1.2.3", " 1", "1 ", "1,5", "ten", "1e", "--1", "1e999", "0x", "1_000"} {
		do(t, conn, "ZADD", "zset_key", score, "member")
		expectReply(t, conn, "-ERR value is not a valid float\r\n")
	}

	// A command that fails creates nothing.
	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+none\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestZAddWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ZADD"}, {"ZADD", "zset_key"}, {"ZADD", "zset_key", "1"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zadd' command\r\n")
	}

	// A score always comes with a member.
	do(t, conn, "ZADD", "zset_key", "1", "a", "2")
	expectReply(t, conn, "-ERR syntax error\r\n")
	// That is checked before the scores are.
	do(t, conn, "ZADD", "zset_key", "one", "a", "2")
	expectReply(t, conn, "-ERR syntax error\r\n")

	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+none\r\n")
}

func TestZAddOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZADD", key, "1", "member")
		expectReply(t, conn, wrongTypeError)
	}
	// The score is looked at first.
	do(t, conn, "ZADD", "string_key", "abc", "member")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")

	// The keys are as they were.
	do(t, conn, "GET", "string_key")
	expectReply(t, conn, bulk("value"))
	do(t, conn, "TYPE", "list_key")
	expectReply(t, conn, "+list\r\n")
	do(t, conn, "TYPE", "stream_key")
	expectReply(t, conn, "+stream\r\n")
}

func TestCommandsOfOtherTypesOnASortedSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "1", "member")
	expectReply(t, conn, ":1\r\n")

	for _, command := range [][]string{
		{"GET", "zset_key"},
		{"INCR", "zset_key"},
		{"RPUSH", "zset_key", "a"},
		{"LPUSH", "zset_key", "a"},
		{"LRANGE", "zset_key", "0", "-1"},
		{"LLEN", "zset_key"},
		{"LPOP", "zset_key"},
		{"BLPOP", "zset_key", "0.01"},
		{"XADD", "zset_key", "1-1", "k", "v"},
		{"XRANGE", "zset_key", "-", "+"},
		{"XREAD", "STREAMS", "zset_key", "0-0"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, wrongTypeError)
	}

	do(t, conn, "TYPE", "zset_key")
	expectReply(t, conn, "+zset\r\n")
}

func TestSetReplacesASortedSet(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "ZADD", "key", "1", "member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "ZADD", "key", "1", "member")
	expectReply(t, conn, wrongTypeError)
}

func TestZAddOnAKeyThatExpired(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "key", "value", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	clock.Advance(100 * time.Millisecond)

	// The string is gone: the key is free for a sorted set.
	do(t, conn, "ZADD", "key", "1", "member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+zset\r\n")
	// Which doesn't expire.
	clock.Advance(time.Hour)
	do(t, conn, "TYPE", "key")
	expectReply(t, conn, "+zset\r\n")
}

func TestZAddCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"ZADD", "zadd", "ZAdd"} {
		// The key and the member are used as they are.
		do(t, conn, name, "Key-of-"+name, "1", "Member")
		expectReply(t, conn, ":1\r\n")
	}
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("Key-of-ZADD", "Key-of-ZAdd", "Key-of-zadd"))
}

func TestZAddMembersAreBinarySafe(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	for _, member := range []string{"", "two words", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n", "10.0"} {
		key := "zset_" + member
		do(t, conn, "ZADD", key, "1", member)
		expectReply(t, conn, ":1\r\n")
		want := []scoredMember{{member, 1}}
		if got := sortedSetMembers(t, srv, key); !reflect.DeepEqual(got, want) {
			t.Errorf("sorted set %q holds %v, want %v", key, got, want)
		}
	}
}

func TestZAddIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)

	conn := dial(t, addr)
	do(t, conn, "ZADD", "zset_key", "10.0", "zset_member")
	expectReply(t, conn, ":1\r\n")
	// A command that fails is neither logged nor propagated.
	do(t, conn, "ZADD", "zset_key", "abc", "other")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")

	// The command is logged and propagated as it was received.
	if got, want := readFile(t, incrPath), encodeCommand("ZADD", "zset_key", "10.0", "zset_member"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectPropagated(t, replica, "ZADD", "zset_key", "10.0", "zset_member")
	expectNoReply(t, replica, 50*time.Millisecond)

	// A server that restarts finds the sorted set again.
	restarted := restartWithAOF(t, srv, args...)
	want := []scoredMember{{"zset_member", 10}}
	if got := sortedSetMembers(t, restarted, "zset_key"); !reflect.DeepEqual(got, want) {
		t.Errorf("sorted set holds %v after a restart, want %v", got, want)
	}
}

func TestZAddIsAppliedByReplicas(t *testing.T) {
	master := startFakeMaster(t)
	srv, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"ZADD", "zset_key", "10.0", "zset_member"})

	conn := dial(t, addr)
	expectEventually(t, conn, "+zset\r\n", "TYPE", "zset_key")
	want := []scoredMember{{"zset_member", 10}}
	if got := sortedSetMembers(t, srv, "zset_key"); !reflect.DeepEqual(got, want) {
		t.Errorf("sorted set holds %v on the replica, want %v", got, want)
	}
}

func TestZAddInATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "ZADD", "zset_key", "10.0", "zset_member")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "ZADD", "other_key", "abc", "member")
	expectReply(t, conn, "+QUEUED\r\n")

	// Nothing is created before the transaction is executed.
	do(t, other, "TYPE", "zset_key")
	expectReply(t, other, "+none\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", "-ERR value is not a valid float\r\n"))
	do(t, other, "TYPE", "zset_key")
	expectReply(t, other, "+zset\r\n")
}

func TestZAddModifiesAWatchedKey(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "WATCH", "zset_key")
	expectReply(t, conn, "+OK\r\n")
	do(t, other, "ZADD", "zset_key", "10.0", "zset_member")
	expectReply(t, other, ":1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+QUEUED\r\n")
	// The key was created while it was watched: the transaction is aborted.
	do(t, conn, "EXEC")
	expectReply(t, conn, "*-1\r\n")
}
