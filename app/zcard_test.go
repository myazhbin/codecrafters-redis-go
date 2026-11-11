package main

import (
	"math/rand"
	"net"
	"strconv"
	"testing"
	"time"
)

// expectZCard checks the cardinality ZCARD reports for the key.
func expectZCard(t *testing.T, conn net.Conn, key string, want int) {
	t.Helper()

	do(t, conn, "ZCARD", key)
	expectReply(t, conn, ":"+strconv.Itoa(want)+"\r\n")
}

func TestZCard(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "20.0", "zset_member1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "30.1", "zset_member2")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "40.2", "zset_member3")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "50.3", "zset_member4")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "ZCARD", "zset_key")
	expectReply(t, conn, ":4\r\n")

	// Updating the score of a member doesn't add a member.
	do(t, conn, "ZADD", "zset_key", "100.0", "zset_member1")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZCARD", "zset_key")
	expectReply(t, conn, ":4\r\n")

	// A sorted set that doesn't exist has no member.
	do(t, conn, "ZCARD", "missing_key")
	expectReply(t, conn, ":0\r\n")
}

func TestZCardCountsEachMemberOnce(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1.2", "one")
	expectZCard(t, conn, "zset_key", 1)
	zadd(t, conn, "zset_key", "2.2", "two")
	expectZCard(t, conn, "zset_key", 2)

	// However many times a member is added, and whatever its score, it is in
	// the set once.
	for _, score := range []string{"2.2", "5", "-1", "inf"} {
		do(t, conn, "ZADD", "zset_key", score, "two")
		expectReply(t, conn, ":0\r\n")
		expectZCard(t, conn, "zset_key", 2)
	}
	// Members with the same score are different members.
	zadd(t, conn, "zset_key", "1.2", "uno")
	expectZCard(t, conn, "zset_key", 3)

	// Several members can be added at once.
	do(t, conn, "ZADD", "zset_key", "3", "three", "1", "one", "4", "four", "4", "four")
	expectReply(t, conn, ":2\r\n")
	expectZCard(t, conn, "zset_key", 5)
}

func TestZCardOfAMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "member")
	for _, key := range []string{"missing_key", "ZSET_KEY", "zset_key ", "member", ""} {
		expectZCard(t, conn, key, 0)
	}

	// Counting the members of a set doesn't create it.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("zset_key"))
	do(t, conn, "TYPE", "missing_key")
	expectReply(t, conn, "+none\r\n")
}

func TestZCardOfARandomSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The count can't be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for range 3 {
		key, members := randomWord(t), 1+rng.Intn(30)
		for i := range members {
			zadd(t, conn, key, strconv.Itoa(rng.Intn(5)), "member-"+strconv.Itoa(i))
		}
		expectZCard(t, conn, key, members)
	}
}

func TestZCardOfALargeSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const members = 3000
	for i := range members {
		zadd(t, conn, "zset_key", strconv.Itoa(i%7), "member-"+strconv.Itoa(i))
	}
	expectZCard(t, conn, "zset_key", members)
}

func TestSortedSetsHaveTheirOwnCardinality(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "first", "1", "a", "2", "b", "3", "c")
	zadd(t, conn, "second", "1", "a")
	expectZCard(t, conn, "first", 3)
	expectZCard(t, conn, "second", 1)
	zadd(t, conn, "second", "2", "b")
	expectZCard(t, conn, "first", 3)
	expectZCard(t, conn, "second", 2)
}

func TestZCardOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZCARD", key)
		expectReply(t, conn, wrongTypeError)
	}
	// LLEN counts the elements of a list, not the members of a sorted set.
	zadd(t, conn, "zset_key", "1", "a")
	do(t, conn, "LLEN", "zset_key")
	expectReply(t, conn, wrongTypeError)
}

func TestZCardWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ZCARD"}, {"ZCARD", "zset_key", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zcard' command\r\n")
	}
}

func TestZCardCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	for _, name := range []string{"ZCARD", "zcard", "ZCard"} {
		do(t, conn, name, "zset_key")
		expectReply(t, conn, ":2\r\n")
	}
}

func TestZCardInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10", "a")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZCARD", "zset_key"},
		{"ZADD", "zset_key", "5", "b", "6", "c"},
		{"ZCARD", "zset_key"},
		{"ZADD", "zset_key", "7", "b"},
		{"ZCARD", "zset_key"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", ":2\r\n", ":3\r\n", ":0\r\n", ":3\r\n"))
}

func TestZCardIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1", "a")
	expectPropagated(t, replica, "ZADD", "zset_key", "1", "a")
	do(t, watcher, "WATCH", "zset_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectZCard(t, conn, "zset_key", 1)
	expectZCard(t, conn, "missing_key", 0)

	// Counting members is neither logged nor propagated, and doesn't count
	// as a modification of the key.
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

func TestZCardAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "1", "a", "2", "b")
	zadd(t, conn, "zset_key", "3", "c")
	do(t, conn, "ZADD", "zset_key", "9", "a")
	expectReply(t, conn, ":0\r\n")

	// The set is rebuilt from the log, with as many members.
	conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
	expectZCard(t, conn, "zset_key", 3)
	expectZRange(t, conn, "zset_key", 0, -1, "b", "c", "a")
}
