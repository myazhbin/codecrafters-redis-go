package main

import (
	"math"
	"net"
	"strconv"
	"testing"
	"time"
)

// geoPos sends GEOPOS for the members and returns its reply: for each of
// them, in order, its longitude and its latitude as they are written in the
// reply, or nil for a member that has no position.
func geoPos(t *testing.T, conn net.Conn, key string, members ...string) []*[2]string {
	t.Helper()

	do(t, conn, append([]string{"GEOPOS", key}, members...)...)
	if got, want := readReplyLine(t, conn), "*"+strconv.Itoa(len(members)); got != want {
		t.Fatalf("reply to GEOPOS starts with %q, want %q: one entry per member", got, want)
	}
	positions := make([]*[2]string, len(members))
	for i := range positions {
		switch header := readReplyLine(t, conn); header {
		case "*-1":
		case "*2":
			positions[i] = &[2]string{readBulkString(t, conn), readBulkString(t, conn)}
		default:
			t.Fatalf("entry %d of the reply to GEOPOS starts with %q, want an array of 2 elements or a null array", i, header)
		}
	}
	return positions
}

// expectPosition checks that GEOPOS places the member within a meter or so
// of the given coordinates: positions don't have the precision of the
// coordinates they come from.
func expectPosition(t *testing.T, conn net.Conn, key, member string, longitude, latitude float64) {
	t.Helper()

	position := geoPos(t, conn, key, member)[0]
	if position == nil {
		t.Fatalf("%q has no position in %q, want one close to (%v, %v)", member, key, longitude, latitude)
	}
	gotLongitude, err1 := strconv.ParseFloat(position[0], 64)
	gotLatitude, err2 := strconv.ParseFloat(position[1], 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("position of %q is (%q, %q), want two numbers", member, position[0], position[1])
	}
	if math.Abs(gotLongitude-longitude) > 1e-5 || math.Abs(gotLatitude-latitude) > 1e-5 {
		t.Errorf("position of %q is (%s, %s), want it close to (%v, %v)", member, position[0], position[1], longitude, latitude)
	}
}

func TestGeoPos(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "location_key", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "location_key", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")

	// One entry per location, in the order they are asked for: an array made
	// of the longitude and the latitude, as bulk strings.
	positions := geoPos(t, conn, "location_key", "London", "Munich")
	for i, position := range positions {
		if position == nil {
			t.Fatalf("entry %d is a null array, want a longitude and a latitude", i)
		}
		for _, coordinate := range position {
			if _, err := strconv.ParseFloat(coordinate, 64); err != nil {
				t.Errorf("entry %d holds %q, want a floating-point number", i, coordinate)
			}
		}
	}

	// A location that doesn't exist has a null array for entry.
	do(t, conn, "GEOPOS", "location_key", "missing_location")
	expectReply(t, conn, "*1\r\n*-1\r\n")
	// So does every location of a key that doesn't exist.
	do(t, conn, "GEOPOS", "missing_key", "London", "Munich")
	expectReply(t, conn, "*2\r\n*-1\r\n*-1\r\n")
}

func TestGeoPosIsWhereTheLocationWasAdded(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":2\r\n")
	expectPosition(t, conn, "places", "London", -0.0884948, 51.506479)
	expectPosition(t, conn, "places", "Munich", 11.5030378, 48.164271)

	for _, city := range geoTestCities {
		do(t, conn, "GEOADD", "cities", city.longitude, city.latitude, city.name)
		expectReply(t, conn, ":1\r\n")
	}
	for _, city := range geoTestCities {
		longitude, _ := strconv.ParseFloat(city.longitude, 64)
		latitude, _ := strconv.ParseFloat(city.latitude, 64)
		expectPosition(t, conn, "cities", city.name, longitude, latitude)
	}
}

func TestGeoPosOfSeveralLocations(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":2\r\n")

	// The entries are in the order of the request, whether the locations
	// exist or not, and a location that is asked for twice is there twice.
	positions := geoPos(t, conn, "places", "Munich", "nowhere", "London", "", "Munich", "london")
	for i, exists := range []bool{true, false, true, false, true, false} {
		if (positions[i] != nil) != exists {
			t.Errorf("entry %d has a position: %t, want %t", i, positions[i] != nil, exists)
		}
	}
	if *positions[0] != *positions[4] {
		t.Errorf("Munich is at %v, then at %v, in the same reply", *positions[0], *positions[4])
	}
	if *positions[0] == *positions[2] {
		t.Errorf("Munich and London are both at %v", *positions[0])
	}

	// Without any location to look for, the reply is an empty array.
	do(t, conn, "GEOPOS", "places")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "GEOPOS", "missing_key")
	expectReply(t, conn, "*0\r\n")
}

func TestGeoPosFollowsTheLocation(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "somewhere")
	expectReply(t, conn, ":1\r\n")
	expectPosition(t, conn, "places", "somewhere", 2.2944692, 48.8584625)

	do(t, conn, "GEOADD", "places", "139.6917", "35.6895", "somewhere")
	expectReply(t, conn, ":0\r\n")
	expectPosition(t, conn, "places", "somewhere", 139.6917, 35.6895)

	// A location that is removed from the set has no position anymore.
	do(t, conn, "ZREM", "places", "somewhere")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOPOS", "places", "somewhere")
	expectReply(t, conn, "*1\r\n*-1\r\n")
}

func TestGeoPosNamesAreExact(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	for _, member := range []string{"london", "LONDON", "London ", " London", "Lond", "Londons", "", "*"} {
		do(t, conn, "GEOPOS", "places", member)
		expectReply(t, conn, "*1\r\n*-1\r\n")
	}
	for _, key := range []string{"Places", "places ", "London"} {
		do(t, conn, "GEOPOS", key, "London")
		expectReply(t, conn, "*1\r\n*-1\r\n")
	}
	// Looking for positions creates nothing.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("places"))

	// Names can be any bytes.
	for _, member := range []string{"", "Statue of Liberty", "a\r\nb", "a\x00b", "München ✓"} {
		do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", member)
		expectReply(t, conn, ":1\r\n")
		expectPosition(t, conn, "places", member, 11.5030378, 48.164271)
	}
}

func TestGeoPosOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "string_key", "London")
	do(t, conn, "RPUSH", "list_key", "London")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "London", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "GEOPOS", key, "London")
		expectReply(t, conn, wrongTypeError)
		do(t, conn, "GEOPOS", key)
		expectReply(t, conn, wrongTypeError)
	}
}

func TestGeoPosWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOPOS")
	expectReply(t, conn, "-ERR wrong number of arguments for 'geopos' command\r\n")
}

func TestGeoPosCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	for _, name := range []string{"GEOPOS", "geopos", "GeoPos"} {
		do(t, conn, name, "places", "nowhere")
		expectReply(t, conn, "*1\r\n*-1\r\n")
		do(t, conn, name, "places", "London")
		if got := readReplyLine(t, conn); got != "*1" {
			t.Fatalf("reply to %s starts with %q, want *1", name, got)
		}
		drainPendingReply(t, conn)
	}
}

func TestGeoPosInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GEOPOS", "places", "London"},
		{"GEOADD", "places", "-180", "-85.05112878", "corner"},
		{"GEOPOS", "places", "London", "corner"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	// The reply to EXEC holds the replies of the three commands.
	if got := readReplyLine(t, conn); got != "*3" {
		t.Fatalf("reply to EXEC starts with %q, want *3", got)
	}
	expectReply(t, conn, "*1\r\n*-1\r\n"+":1\r\n"+"*2\r\n*-1\r\n*2\r\n")
	longitude, latitude := readBulkString(t, conn), readBulkString(t, conn)
	if lon, err := strconv.ParseFloat(longitude, 64); err != nil || math.Abs(lon+180) > 1e-5 {
		t.Errorf("longitude of the corner = %q, want it close to -180", longitude)
	}
	if lat, err := strconv.ParseFloat(latitude, 64); err != nil || math.Abs(lat+85.05112878) > 1e-5 {
		t.Errorf("latitude of the corner = %q, want it close to -85.05112878", latitude)
	}
}

func TestGeoPosIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	expectPropagated(t, replica, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	do(t, watcher, "WATCH", "places", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectPosition(t, conn, "places", "London", -0.0884948, 51.506479)
	do(t, conn, "GEOPOS", "missing_key", "London")
	expectReply(t, conn, "*1\r\n*-1\r\n")

	// Reading a position is neither logged nor propagated, and doesn't
	// count as a modification of the key.
	if got, want := readFile(t, incrPath), encodeCommand("GEOADD", "places", "-0.0884948", "51.506479", "London"); got != want {
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
