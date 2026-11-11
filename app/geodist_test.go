package main

import (
	"math"
	"net"
	"strconv"
	"testing"
	"time"
)

// expectGeoDist checks the distance GEODIST reports between two members, as
// it is written in the reply. unit is optional.
func expectGeoDist(t *testing.T, conn net.Conn, key, from, to, want string, unit ...string) {
	t.Helper()

	do(t, conn, append([]string{"GEODIST", key, from, to}, unit...)...)
	expectReply(t, conn, bulk(want))
}

// addGeoTestCities stores the reference cities at key.
func addGeoTestCities(t *testing.T, conn net.Conn, key string) {
	t.Helper()

	for _, city := range geoTestCities {
		do(t, conn, "GEOADD", key, city.longitude, city.latitude, city.name)
		expectReply(t, conn, ":1\r\n")
	}
}

func TestGeoDist(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")

	// The distance is in meters, as a bulk string.
	do(t, conn, "GEODIST", "places", "Munich", "Paris")
	expectReply(t, conn, "$11\r\n682477.7582\r\n")
}

func TestGeoDistIsTheSameBothWays(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":2\r\n")
	expectGeoDist(t, conn, "places", "Munich", "Paris", "682477.7582")
	expectGeoDist(t, conn, "places", "Paris", "Munich", "682477.7582")
	// A place is at no distance from itself.
	expectGeoDist(t, conn, "places", "Paris", "Paris", "0.0000")
}

func TestGeoDistUnits(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The example of the Redis documentation.
	do(t, conn, "GEOADD", "Sicily", "13.361389", "38.115556", "Palermo", "15.087269", "37.502669", "Catania")
	expectReply(t, conn, ":2\r\n")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "166274.1516")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "166274.1516", "m")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "166.2742", "km")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "103.3182", "mi")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "545518.8700", "ft")

	// The names of the units are case-insensitive.
	for _, unit := range []string{"KM", "Km", "kM"} {
		expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "166.2742", unit)
	}
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "166274.1516", "M")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "103.3182", "MI")
	expectGeoDist(t, conn, "Sicily", "Palermo", "Catania", "545518.8700", "FT")
}

func TestGeoDistOfAMissingMemberOrKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":2\r\n")

	// Both members have to be there for a distance to exist.
	for _, members := range [][2]string{
		{"Munich", "nowhere"}, {"nowhere", "Paris"}, {"nowhere", "elsewhere"}, {"munich", "Paris"}, {"Munich", "Paris "}, {"", "Paris"},
	} {
		do(t, conn, "GEODIST", "places", members[0], members[1])
		expectReply(t, conn, "$-1\r\n")
		do(t, conn, "GEODIST", "places", members[0], members[1], "km")
		expectReply(t, conn, "$-1\r\n")
	}
	do(t, conn, "GEODIST", "missing_key", "Munich", "Paris")
	expectReply(t, conn, "$-1\r\n")

	// Nothing was created.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("places"))
}

// haversine is an independent implementation of the distance between two
// points of a sphere that has the radius of the Earth, in meters. Its
// arguments are in degrees.
func haversine(lon1, lat1, lon2, lat2 float64) float64 {
	const radius = 6372797.560856
	toRadians := func(degrees float64) float64 { return degrees * math.Pi / 180 }

	dLat, dLon := toRadians(lat2-lat1), toRadians(lon2-lon1)
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(toRadians(lat1))*math.Cos(toRadians(lat2))*math.Pow(math.Sin(dLon/2), 2)
	return 2 * radius * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

func TestGeoDistBetweenKnownCities(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoTestCities(t, conn, "cities")

	// Where each city is, according to the server.
	type position struct{ longitude, latitude float64 }
	positions := make(map[string]position)
	for _, city := range geoTestCities {
		p := geoPos(t, conn, "cities", city.name)[0]
		longitude, _ := strconv.ParseFloat(p[0], 64)
		latitude, _ := strconv.ParseFloat(p[1], 64)
		positions[city.name] = position{longitude, latitude}
	}

	// The distance between two cities is the one between those positions,
	// whichever way it is computed.
	for _, from := range geoTestCities {
		for _, to := range geoTestCities {
			do(t, conn, "GEODIST", "cities", from.name, to.name)
			got, err := strconv.ParseFloat(readBulkString(t, conn), 64)
			if err != nil {
				t.Fatalf("distance from %s to %s isn't a number: %v", from.name, to.name, err)
			}
			a, b := positions[from.name], positions[to.name]
			want := haversine(a.longitude, a.latitude, b.longitude, b.latitude)
			if math.Abs(got-want) > 0.001 {
				t.Errorf("distance from %s to %s = %.4f, want %.4f", from.name, to.name, got, want)
			}
		}
	}

	// A few of them, as a traveller knows them: within a kilometer or so.
	for _, tt := range []struct {
		from, to   string
		kilometers float64
	}{
		{"London", "Paris", 343},
		{"Berlin", "Vienna", 523},
		{"New York", "London", 5571},
		{"Tokyo", "Sydney", 7826},
		{"Beijing", "Bangkok", 3299},
		{"New Delhi", "Kathmandu", 800},
	} {
		do(t, conn, "GEODIST", "cities", tt.from, tt.to, "km")
		got, _ := strconv.ParseFloat(readBulkString(t, conn), 64)
		if math.Abs(got-tt.kilometers) > 5 {
			t.Errorf("distance from %s to %s = %.1f km, want about %.0f km", tt.from, tt.to, got, tt.kilometers)
		}
	}
}

func TestGeoDistOnTheSameMeridian(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Two places that have the same longitude are apart by the difference
	// of their latitudes: one degree is 111 kilometers and a bit.
	do(t, conn, "GEOADD", "places", "10", "0", "equator", "10", "1", "one degree north", "10", "-45", "far south")
	expectReply(t, conn, ":3\r\n")

	for _, tt := range []struct {
		from, to string
		degrees  float64
	}{
		{"equator", "one degree north", 1},
		{"one degree north", "equator", 1},
		{"equator", "far south", 45},
		{"one degree north", "far south", 46},
	} {
		do(t, conn, "GEODIST", "places", tt.from, tt.to)
		got, _ := strconv.ParseFloat(readBulkString(t, conn), 64)
		want := 6372797.560856 * tt.degrees * math.Pi / 180
		// Positions are known to within less than a meter.
		if math.Abs(got-want) > 1 {
			t.Errorf("distance from %s to %s = %.4f, want about %.4f", tt.from, tt.to, got, want)
		}
	}
}

func TestGeoDistAcrossTheWorld(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Half of the circumference of the Earth is as far as two places can be.
	do(t, conn, "GEOADD", "places", "0", "0", "origin", "180", "0", "antipode", "-180", "0", "antipode west", "90", "0", "quarter")
	expectReply(t, conn, ":4\r\n")
	halfway := math.Pi * 6372797.560856

	for _, tt := range []struct {
		from, to string
		want     float64
	}{
		{"origin", "antipode", halfway},
		{"origin", "antipode west", halfway},
		{"origin", "quarter", halfway / 2},
		{"quarter", "antipode", halfway / 2},
		// East and west meet on the other side of the world.
		{"antipode", "antipode west", 0},
	} {
		do(t, conn, "GEODIST", "places", tt.from, tt.to)
		got, _ := strconv.ParseFloat(readBulkString(t, conn), 64)
		if math.Abs(got-tt.want) > 2 {
			t.Errorf("distance from %s to %s = %.4f, want about %.4f", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestGeoDistFollowsTheLocations(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "here", "2.2944692", "48.8584625", "there")
	expectReply(t, conn, ":2\r\n")
	expectGeoDist(t, conn, "places", "here", "there", "682477.7582")

	// A member that moves is at another distance.
	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "there")
	expectReply(t, conn, ":0\r\n")
	expectGeoDist(t, conn, "places", "here", "there", "0.0000")
	// And one that is removed is at none.
	do(t, conn, "ZREM", "places", "there")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEODIST", "places", "here", "there")
	expectReply(t, conn, "$-1\r\n")
}

func TestGeoDistBetweenMembersAddedWithZAdd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Munich is given the score GEOADD computes for it, and Paris the one
	// that is known for it.
	do(t, conn, "GEOADD", "reference", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "ZSCORE", "reference", "Munich")
	zadd(t, conn, "places", readBulkString(t, conn), "Munich")
	zadd(t, conn, "places", "3663832614298053", "Paris")

	// A location is its score, however it got it.
	expectGeoDist(t, conn, "places", "Munich", "Paris", "682477.7582")
}

func TestGeoDistInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":2\r\n")

	for _, command := range [][]string{{"GEODIST"}, {"GEODIST", "places"}, {"GEODIST", "places", "Munich"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'geodist' command\r\n")
	}
	do(t, conn, "GEODIST", "places", "Munich", "Paris", "km", "extra")
	expectReply(t, conn, "-ERR syntax error\r\n")

	// The unit is checked whether the key and the members exist or not.
	for _, key := range []string{"places", "missing_key"} {
		for _, unit := range []string{"meters", "kilometers", "", "k", "cm", "nm", "yd", "1"} {
			do(t, conn, "GEODIST", key, "Munich", "nowhere", unit)
			expectReply(t, conn, "-ERR unsupported unit provided. please use M, KM, FT, MI\r\n")
		}
	}
}

func TestGeoDistOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "string_key", "Munich")
	do(t, conn, "RPUSH", "list_key", "Munich", "Paris")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "Munich", "Paris")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "GEODIST", key, "Munich", "Paris")
		expectReply(t, conn, wrongTypeError)
		// The unit is looked at first.
		do(t, conn, "GEODIST", key, "Munich", "Paris", "parsecs")
		expectReply(t, conn, "-ERR unsupported unit provided. please use M, KM, FT, MI\r\n")
	}
}

func TestGeoDistCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":2\r\n")
	for _, name := range []string{"GEODIST", "geodist", "GeoDist"} {
		do(t, conn, name, "places", "Munich", "Paris")
		expectReply(t, conn, bulk("682477.7582"))
	}
}

func TestGeoDistInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GEODIST", "places", "Munich", "Paris"},
		{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"},
		{"GEODIST", "places", "Munich", "Paris"},
		{"GEODIST", "places", "Munich", "Paris", "km"},
		{"GEODIST", "places", "Munich", "Paris", "leagues"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array("$-1\r\n", ":1\r\n", bulk("682477.7582"), bulk("682.4778"),
		"-ERR unsupported unit provided. please use M, KM, FT, MI\r\n"))
}

func TestGeoDistIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	command := []string{"GEOADD", "places", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris"}
	do(t, conn, command...)
	expectReply(t, conn, ":2\r\n")
	expectPropagated(t, replica, command...)
	do(t, watcher, "WATCH", "places", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectGeoDist(t, conn, "places", "Munich", "Paris", "682477.7582")
	do(t, conn, "GEODIST", "missing_key", "Munich", "Paris")
	expectReply(t, conn, "$-1\r\n")

	// Measuring a distance is neither logged nor propagated, and doesn't
	// count as a modification of the key.
	if got, want := readFile(t, incrPath), encodeCommand(command...); got != want {
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
