package main

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// locationNames returns the names of the locations stored at key on srv, in
// alphabetical order.
func locationNames(t *testing.T, srv *Server, key string) []string {
	t.Helper()

	var names []string
	for _, m := range sortedSetMembers(t, srv, key) {
		names = append(names, m.member)
	}
	sort.Strings(names)
	return names
}

func TestGeoAddStoresTheLocationInASortedSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")

	// The location is a member of a sorted set.
	do(t, conn, "ZRANGE", "places", "0", "-1")
	expectReply(t, conn, "*1\r\n$5\r\nParis\r\n")
	do(t, conn, "TYPE", "places")
	expectReply(t, conn, "+zset\r\n")
	expectZCard(t, conn, "places", 1)
	do(t, conn, "ZRANK", "places", "Paris")
	expectReply(t, conn, ":0\r\n")
}

func TestGeoAddStoresEveryLocation(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "-0.127758", "51.507351", "London")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich", "13.361389", "38.115556", "Palermo")
	expectReply(t, conn, ":2\r\n")

	if got, want := locationNames(t, srv, "places"), []string{"London", "Munich", "Palermo", "Paris"}; !reflect.DeepEqual(got, want) {
		t.Errorf("locations = %q, want %q", got, want)
	}
	expectZCard(t, conn, "places", 4)

	// Each key has its own locations.
	do(t, conn, "GEOADD", "other", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "other", 0, -1, "Paris")
	expectZCard(t, conn, "places", 4)
}

func TestGeoAddOfALocationThatIsAlreadyStored(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	// The name is in the set already: nothing is added.
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":0\r\n")
	expectZRange(t, conn, "places", 0, -1, "Paris")

	// A name has one location: given other coordinates, it moves there.
	before := sortedSetMembers(t, srv, "places")
	do(t, conn, "GEOADD", "places", "-95.5555", "33.6609", "Paris")
	expectReply(t, conn, ":0\r\n")
	after := sortedSetMembers(t, srv, "places")
	if len(after) != 1 || after[0].member != "Paris" || after[0].score == before[0].score {
		t.Errorf("after the location moved the set holds %v, want Paris with another score than %v", after, before[0].score)
	}

	// Only the names that are new count.
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "-0.127758", "51.507351", "London", "-0.127758", "51.507351", "London")
	expectReply(t, conn, ":1\r\n")
	expectZCard(t, conn, "places", 2)
}

func TestGeoAddStoresNothingWhenALocationIsInvalid(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "200", "100", "nowhere")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "200", "100", "nowhere")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "east", "north", "nowhere")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")

	// Not even the locations that were valid, and the key wasn't created.
	do(t, conn, "TYPE", "places")
	expectReply(t, conn, "+none\r\n")
	expectZRange(t, conn, "places", 0, -1)

	// With a key that exists, the set is left as it is.
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "-0.127758", "51.507351", "London", "200", "100", "nowhere")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	expectZRange(t, conn, "places", 0, -1, "Paris")
}

func TestGeoAddNamesAreBinarySafe(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	names := []string{"", "Statue of Liberty", "a\r\nb", "a\x00b", "München ✓", "*1\r\n$4\r\nPING\r\n"}
	for _, name := range names {
		do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", name)
		expectReply(t, conn, ":1\r\n")
	}
	want := append([]string(nil), names...)
	sort.Strings(want)
	if got := locationNames(t, srv, "places"); !reflect.DeepEqual(got, want) {
		t.Errorf("locations = %q, want %q", got, want)
	}
}

func TestGeoAddOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "string_key", "value")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "GEOADD", key, "2.2944692", "48.8584625", "Paris")
		expectReply(t, conn, wrongTypeError)
		// The coordinates are looked at first.
		do(t, conn, "GEOADD", key, "200", "100", "nowhere")
		expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	}
	do(t, conn, "GET", "string_key")
	expectReply(t, conn, bulk("value"))
}

func TestLocationsAndSortedSetMembersShareTheSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A location can be added to a sorted set made with ZADD, and a member
	// to a set of locations.
	zadd(t, conn, "places", "0", "nowhere")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	zadd(t, conn, "places", "1", "somewhere")
	expectZCard(t, conn, "places", 3)
	expectZRange(t, conn, "places", 0, -1, "nowhere", "somewhere", "Paris")

	// The commands of sorted sets work on locations.
	do(t, conn, "ZREM", "places", "Paris")
	expectReply(t, conn, ":1\r\n")
	expectZRange(t, conn, "places", 0, -1, "nowhere", "somewhere")
	// And a name that was removed is new when it is added again.
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "0", "0", "nowhere")
	expectReply(t, conn, ":1\r\n")

	// Removing the last location deletes the key.
	do(t, conn, "ZREM", "places", "Paris", "nowhere", "somewhere")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "TYPE", "places")
	expectReply(t, conn, "+none\r\n")
}

func TestGeoAddIsAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	args := []string{"--dir", dir, "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn := dial(t, addr)

	commands := [][]string{
		{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"},
		{"GEOADD", "places", "-0.127758", "51.507351", "London", "11.5030378", "48.164271", "Munich"},
		// A location that moves is a modification, even though nothing is
		// added.
		{"GEOADD", "places", "-95.5555", "33.6609", "Paris"},
	}
	want := ""
	for _, command := range commands {
		do(t, conn, command...)
		drainPendingReply(t, conn)
		expectPropagated(t, replica, command...)
		want += encodeCommand(command...)
	}

	// Adding a location where it already is changes nothing: it isn't
	// logged or propagated. Neither is a command that fails.
	do(t, conn, "GEOADD", "places", "-0.127758", "51.507351", "London")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GEOADD", "places", "200", "100", "nowhere")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")

	if got := readFile(t, incrPath); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectNoReply(t, replica, 50*time.Millisecond)

	// A server that restarts finds the same locations, at the same place.
	before := sortedSetMembers(t, srv, "places")
	restarted := restartWithAOF(t, srv, args...)
	if after := sortedSetMembers(t, restarted, "places"); !reflect.DeepEqual(after, before) {
		t.Errorf("after a restart the set holds %v, want %v", after, before)
	}
}

func TestGeoAddIsAppliedByReplicas(t *testing.T) {
	master := startFakeMaster(t)
	srv, addr := startReplicaOf(t, master)
	link := master.acceptReplica()

	link.propagate([]string{"GEOADD", "places", "2.2944692", "48.8584625", "Paris", "-0.127758", "51.507351", "London"})

	conn := dial(t, addr)
	expectEventually(t, conn, ":2\r\n", "ZCARD", "places")
	if got, want := locationNames(t, srv, "places"), []string{"London", "Paris"}; !reflect.DeepEqual(got, want) {
		t.Errorf("locations on the replica = %q, want %q", got, want)
	}
}

func TestGeoAddInATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn, other := dial(t, addr), dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"},
		{"GEOADD", "places", "200", "100", "nowhere"},
		{"GEOADD", "places", "2.2944692", "48.8584625", "Paris", "-0.127758", "51.507351", "London"},
		{"ZCARD", "places"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// Nothing is stored before the transaction is executed.
	do(t, other, "TYPE", "places")
	expectReply(t, other, "+none\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n", ":1\r\n", ":2\r\n"))
}

func TestGeoAddModifiesAWatchedKeyOnlyWhenItChangesSomething(t *testing.T) {
	addr := startTestServer(t)
	conn, watcher := dial(t, addr), dial(t, addr)
	transaction := func(want string) {
		t.Helper()
		do(t, watcher, "MULTI")
		expectReply(t, watcher, "+OK\r\n")
		do(t, watcher, "PING")
		expectReply(t, watcher, "+QUEUED\r\n")
		do(t, watcher, "EXEC")
		expectReply(t, watcher, want)
	}

	// Adding a location and moving one are modifications.
	for _, coordinates := range [][2]string{{"2.2944692", "48.8584625"}, {"-95.5555", "33.6609"}} {
		do(t, watcher, "WATCH", "places")
		expectReply(t, watcher, "+OK\r\n")
		do(t, conn, "GEOADD", "places", coordinates[0], coordinates[1], "Paris")
		drainPendingReply(t, conn)
		transaction("*-1\r\n")
	}

	// Adding a location where it already is isn't.
	do(t, watcher, "WATCH", "places")
	expectReply(t, watcher, "+OK\r\n")
	do(t, conn, "GEOADD", "places", "-95.5555", "33.6609", "Paris")
	expectReply(t, conn, ":0\r\n")
	transaction(array("+PONG\r\n"))
}
