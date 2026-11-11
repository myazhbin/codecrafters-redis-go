package main

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func TestGeoPosDecodesTheScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	zadd(t, conn, "location_key", "3663832614298053", "Foo")
	zadd(t, conn, "location_key", "3876464048901851", "Bar")
	zadd(t, conn, "location_key", "3468915414364476", "Baz")
	zadd(t, conn, "location_key", "3781709020344510", "Caz")

	// The coordinates are the ones the score stands for.
	do(t, conn, "GEOPOS", "location_key", "Foo")
	expectReply(t, conn, "*1\r\n*2\r\n$17\r\n2.294471561908722\r\n$17\r\n48.85846255040141\r\n")
}

func TestGeoPosOfLocationsAddedWithGeoAdd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")

	// Not quite the coordinates that were given: the ones Redis gives back
	// for them.
	do(t, conn, "GEOPOS", "places", "London")
	expectReply(t, conn, array(bulkArray("-0.08849412202835083", "51.50647814139934")))
	do(t, conn, "GEOPOS", "places", "Munich")
	expectReply(t, conn, array(bulkArray("11.503036916255951", "48.16427086232978")))
	do(t, conn, "GEOPOS", "places", "Paris", "nowhere", "London")
	expectReply(t, conn, array(
		bulkArray("2.294471561908722", "48.85846255040141"),
		"*-1\r\n",
		bulkArray("-0.08849412202835083", "51.50647814139934"),
	))
}

func TestGeoPosOfKnownScores(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Locations that are only known by their score are found where the
	// coordinates the score was computed from are, give or take the size of
	// the area a score stands for.
	for _, city := range geoTestCities {
		zadd(t, conn, "places", city.score, city.name)
	}
	for _, city := range geoTestCities {
		longitude, _ := strconv.ParseFloat(city.longitude, 64)
		latitude, _ := strconv.ParseFloat(city.latitude, 64)

		position := geoPos(t, conn, "places", city.name)[0]
		if position == nil {
			t.Fatalf("%s has no position", city.name)
		}
		gotLongitude, _ := strconv.ParseFloat(position[0], 64)
		gotLatitude, _ := strconv.ParseFloat(position[1], 64)
		// The area is 360/2^26 degrees wide and 170.1/2^26 degrees high, and
		// the position is its center.
		if math.Abs(gotLongitude-longitude) > 360.0/(1<<27) || math.Abs(gotLatitude-latitude) > 170.10225756/(1<<27) {
			t.Errorf("position of %s is (%s, %s), which isn't in the same area as (%s, %s)",
				city.name, position[0], position[1], city.longitude, city.latitude)
		}
	}
}

func TestGeoPosGivesCoordinatesThatHaveTheSameScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, score := range []string{"3663832614298053", "3876464048901851", "3468915414364476", "3781709020344510", "0", "1", "4503599627370495"} {
		zadd(t, conn, "scores", score, "member-"+score)
		position := geoPos(t, conn, "scores", "member-"+score)[0]
		if position == nil {
			t.Fatalf("the member with score %s has no position", score)
		}

		// Adding a location at the position that was decoded gives it the
		// score the position was decoded from.
		do(t, conn, "GEOADD", "locations", position[0], position[1], "member-"+score)
		expectReply(t, conn, ":1\r\n")
		expectZScore(t, conn, "locations", "member-"+score, score)
	}
}

func TestLocationOfScore(t *testing.T) {
	// The location of a score has that score: decoding then encoding gives
	// back what one started from, for every score a location can have.
	rng := rand.New(rand.NewSource(1))
	for range 200000 {
		score := float64(rng.Int63n(1 << 52))
		l := locationOfScore(score)
		if l.longitude < minLongitude || l.longitude > maxLongitude || l.latitude < minLatitude || l.latitude > maxLatitude {
			t.Fatalf("locationOfScore(%.0f) = (%v, %v), which is out of range", score, l.longitude, l.latitude)
		}
		if got := l.score(); got != score {
			t.Fatalf("locationOfScore(%.0f) = (%v, %v), whose score is %.0f", score, l.longitude, l.latitude, got)
		}
	}

	// And the location of the score of a location is in the area of that
	// location: less than half the size of an area away, in each direction.
	for range 200000 {
		l := location{
			longitude: minLongitude + rng.Float64()*(maxLongitude-minLongitude),
			latitude:  minLatitude + rng.Float64()*(maxLatitude-minLatitude),
		}
		decoded := locationOfScore(l.score())
		if math.Abs(decoded.longitude-l.longitude) > 360.0/(1<<27) || math.Abs(decoded.latitude-l.latitude) > 170.10225756/(1<<27) {
			t.Fatalf("(%v, %v) is decoded as (%v, %v), which is too far", l.longitude, l.latitude, decoded.longitude, decoded.latitude)
		}
	}
}

func TestGeoPosOfTheCorners(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The lowest score is the area in the south-west corner, the highest one
	// the area in the north-east corner.
	zadd(t, conn, "places", "0", "south-west")
	zadd(t, conn, "places", "4503599627370495", "north-east")
	expectPosition(t, conn, "places", "south-west", -180, -85.05112878)
	expectPosition(t, conn, "places", "north-east", 180, 85.05112878)

	// Locations at the limits are found at the limits.
	do(t, conn, "GEOADD", "limits", "-180", "-85.05112878", "min", "180", "85.05112878", "max")
	expectReply(t, conn, ":2\r\n")
	expectPosition(t, conn, "limits", "min", -180, -85.05112878)
	expectPosition(t, conn, "limits", "max", 180, 85.05112878)
}

func TestGeoPosOfMembersThatAreNotLocations(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A sorted set can hold members whose score isn't the one of a
	// location. They are given coordinates all the same, within the limits
	// coordinates have.
	for i, score := range []string{"-1", "-1e300", "0.5", "12.75", "1e300", "inf", "-inf", "9007199254740993", "18446744073709551616"} {
		member := "member-" + strconv.Itoa(i)
		zadd(t, conn, "scores", score, member)

		position := geoPos(t, conn, "scores", member)[0]
		if position == nil {
			t.Fatalf("the member with score %s has no position", score)
		}
		longitude, err1 := strconv.ParseFloat(position[0], 64)
		latitude, err2 := strconv.ParseFloat(position[1], 64)
		if err1 != nil || err2 != nil || longitude < -180 || longitude > 180 || latitude < -85.05112878 || latitude > 85.05112878 {
			t.Errorf("the member with score %s is at (%s, %s), want coordinates within the limits", score, position[0], position[1])
		}
	}
}

func TestGeoPosAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
	zadd(t, conn, "places", "3663832614298053", "Foo")

	conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
	do(t, conn, "GEOPOS", "places", "Munich", "Foo")
	expectReply(t, conn, array(
		bulkArray("11.503036916255951", "48.16427086232978"),
		bulkArray("2.294471561908722", "48.85846255040141"),
	))
}
