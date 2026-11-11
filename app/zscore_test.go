package main

import (
	"math"
	"math/rand"
	"net"
	"strconv"
	"testing"
	"time"
)

// expectZScore checks the score ZSCORE reports for the member, as it is
// written in the reply.
func expectZScore(t *testing.T, conn net.Conn, key, member, want string) {
	t.Helper()

	do(t, conn, "ZSCORE", key, member)
	expectReply(t, conn, bulk(want))
}

func TestZScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "ZADD", "zset_key", "20.0", "zset_member1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "30.1", "zset_member2")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "40.2", "zset_member3")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZADD", "zset_key", "50.3", "zset_member4")
	expectReply(t, conn, ":1\r\n")

	// The score is a bulk string.
	do(t, conn, "ZSCORE", "zset_key", "zset_member2")
	expectReply(t, conn, "$4\r\n30.1\r\n")

	do(t, conn, "ZADD", "zset_key", "100.99", "zset_member2")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "ZSCORE", "zset_key", "zset_member2")
	expectReply(t, conn, "$6\r\n100.99\r\n")

	// There is no score for a member that isn't in the set, or for a set
	// that doesn't exist.
	do(t, conn, "ZSCORE", "zset_key", "zset_member100")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "ZSCORE", "missing_key", "member")
	expectReply(t, conn, "$-1\r\n")
}

func TestZScoreOfEachMember(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "24.34", "one")
	zadd(t, conn, "zset_key", "90.34", "two")
	expectZScore(t, conn, "zset_key", "one", "24.34")
	expectZScore(t, conn, "zset_key", "two", "90.34")
	do(t, conn, "ZSCORE", "zset_key", "three")
	expectReply(t, conn, "$-1\r\n")

	// Members that have the same score are still different members.
	zadd(t, conn, "zset_key", "24.34", "uno")
	expectZScore(t, conn, "zset_key", "uno", "24.34")
	do(t, conn, "ZADD", "zset_key", "1.5", "uno")
	expectReply(t, conn, ":0\r\n")
	expectZScore(t, conn, "zset_key", "uno", "1.5")
	expectZScore(t, conn, "zset_key", "one", "24.34")
}

func TestZScoreIsWrittenLikeRedisDoes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		added string
		want  string
	}{
		// A whole number has no fraction, however it was given.
		{"20.0", "20"},
		{"10", "10"},
		{"1e3", "1000"},
		{"-7.00", "-7"},
		{"0", "0"},
		{"0.0", "0"},
		{"-0", "-0"},
		{"1000000000000000", "1000000000000000"},
		{"4611686018427387904", "4611686018427387904"},
		// Any other number has the digits it takes, and no more.
		{"30.1", "30.1"},
		{"100.99", "100.99"},
		{"-3.5", "-3.5"},
		{".5", "0.5"},
		{"5.25e2", "525"},
		{"1.5E-3", "0.0015"},
		{"0.0043", "0.0043"},
		{"0.000001", "0.000001"},
		{"3.141592653589793", "3.141592653589793"},
		{"0.30000000000000004", "0.30000000000000004"},
		{"12345.6789", "12345.6789"},
		{"123456789.123", "123456789.123"},
		// A number that a float64 can't hold exactly is the closest one it
		// can.
		{"0.1000000000000000055511151231257827", "0.1"},
		{"9007199254740993", "9007199254740992"},
		{"123456789012345678", "123456789012345680"},
		// Very small and very large numbers are in scientific notation.
		{"0.0000001", "1e-7"},
		{"0.00000015", "1.5e-7"},
		{"5e-324", "5e-324"},
		{"10000000000000000000", "1e+19"},
		{"-1e19", "-1e+19"},
		{"1e21", "1e+21"},
		{"1e100", "1e+100"},
		{"1.7976931348623157e308", "1.7976931348623157e+308"},
		{"12345.678901234567", "1.2345678901234567e+4"},
		// Infinity is spelled one way.
		{"inf", "inf"},
		{"+inf", "inf"},
		{"Infinity", "inf"},
		{"-inf", "-inf"},
	}
	for _, tt := range tests {
		t.Run(tt.added, func(t *testing.T) {
			// Each score goes to a set of its own: a member that is given a
			// score equal to the one it has keeps the one it has, and -0 is
			// equal to 0.
			key := "zset_" + tt.added
			zadd(t, conn, key, tt.added, "member")
			expectZScore(t, conn, key, "member", tt.want)
		})
	}
}

func TestZScoreKeepsTheSignOfAZeroThatDoesNotChange(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "0", "member")
	do(t, conn, "ZADD", "zset_key", "-0", "member")
	expectReply(t, conn, ":0\r\n")
	expectZScore(t, conn, "zset_key", "member", "0")
}

func TestFormatScoreIsParsedBackToTheSameScore(t *testing.T) {
	check := func(score float64) {
		t.Helper()
		text := formatScore(score)
		back, ok := parseScore(text)
		if !ok || back != score || math.Signbit(back) != math.Signbit(score) {
			t.Fatalf("formatScore(%v) = %q, which is parsed back as %v (valid: %t)", score, text, back, ok)
		}
	}

	for _, score := range []float64{0, math.Copysign(0, -1), 1, -1, 0.1, 1.0 / 3, math.Pi, math.MaxFloat64, -math.MaxFloat64,
		math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), 1 << 53, 1<<53 + 2, 1 << 62, 1 << 63, -(1 << 62), -(1 << 63), 1e19, 1e-7} {
		check(score)
	}

	rng := rand.New(rand.NewSource(1))
	for range 200000 {
		// Any float64 there is, but NaN, which isn't a score.
		if score := math.Float64frombits(rng.Uint64()); !math.IsNaN(score) {
			check(score)
		}
		// And the kind of numbers people use.
		check(float64(rng.Intn(2000000)-1000000) / 100)
		check(float64(rng.Int63()))
		check(rng.NormFloat64() * 1e6)
	}
}

func TestZScoreOfRandomScores(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Neither the members nor the scores can be hardcoded.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for range 20 {
		member := randomWord(t)
		score := strconv.Itoa(1+rng.Intn(999)) + "." + strconv.Itoa(1+rng.Intn(9))
		zadd(t, conn, "zset_key", score, member)
		expectZScore(t, conn, "zset_key", member, score)
	}
}

func TestZScoreFollowsTheUpdatesOfTheScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1.5", "a", "2.5", "b")
	for _, score := range []string{"100.99", "-4.25", "0.001", "7", "2.5", "1.5"} {
		do(t, conn, "ZADD", "zset_key", score, "a")
		expectReply(t, conn, ":0\r\n")
		expectZScore(t, conn, "zset_key", "a", score)
		// The other members keep their score.
		expectZScore(t, conn, "zset_key", "b", "2.5")
	}
}

func TestZScoreOfAMissingMemberOrKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1.5", "member")

	// Names are exact: nothing is trimmed, and the case matters.
	for _, member := range []string{"zset_member100", "Member", "member ", "memb", "members", "", "1.5", "*"} {
		do(t, conn, "ZSCORE", "zset_key", member)
		expectReply(t, conn, "$-1\r\n")
	}
	for _, key := range []string{"missing_key", "ZSET_KEY", "zset_key ", "member", ""} {
		do(t, conn, "ZSCORE", key, "member")
		expectReply(t, conn, "$-1\r\n")
	}

	// Asking for a score creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("zset_key"))
}

func TestZScoreMembersAreBinarySafe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for i, member := range []string{"", "two words", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n"} {
		score := strconv.Itoa(i) + ".5"
		zadd(t, conn, "zset_key", score, member)
		expectZScore(t, conn, "zset_key", member, score)
	}
}

func TestZScoreOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "string_key", "1.5")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "member")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "member", "1.5")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "ZSCORE", key, "member")
		expectReply(t, conn, wrongTypeError)
	}
}

func TestZScoreWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"ZSCORE"}, {"ZSCORE", "zset_key"}, {"ZSCORE", "zset_key", "a", "b"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'zscore' command\r\n")
	}
}

func TestZScoreCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "1.5", "a")
	for _, name := range []string{"ZSCORE", "zscore", "ZScore"} {
		do(t, conn, name, "zset_key", "a")
		expectReply(t, conn, bulk("1.5"))
	}
}

func TestZScoreInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "zset_key", "10.5", "a")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"ZSCORE", "zset_key", "a"},
		{"ZADD", "zset_key", "5.25", "a"},
		{"ZSCORE", "zset_key", "a"},
		{"ZSCORE", "zset_key", "missing"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array(bulk("10.5"), ":0\r\n", bulk("5.25"), "$-1\r\n"))
}

func TestZScoreIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	zadd(t, conn, "zset_key", "1.5", "a")
	expectPropagated(t, replica, "ZADD", "zset_key", "1.5", "a")
	do(t, watcher, "WATCH", "zset_key", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectZScore(t, conn, "zset_key", "a", "1.5")
	do(t, conn, "ZSCORE", "missing_key", "a")
	expectReply(t, conn, "$-1\r\n")

	// Reading a score is neither logged nor propagated, and doesn't count as
	// a modification of the key.
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

func TestZScoreAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	zadd(t, conn, "zset_key", "24.34", "one", "0.30000000000000004", "two")
	do(t, conn, "ZADD", "zset_key", "100.99", "one")
	expectReply(t, conn, ":0\r\n")

	// The scores are logged as they were given, and come back unchanged.
	conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
	expectZScore(t, conn, "zset_key", "one", "100.99")
	expectZScore(t, conn, "zset_key", "two", "0.30000000000000004")
}
