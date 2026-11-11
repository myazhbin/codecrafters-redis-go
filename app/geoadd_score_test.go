package main

import (
	"strconv"
	"testing"
)

// geoTestCities are locations along with the score Redis gives them. They
// come from the documentation of Redis and from the reference the course
// provides for the algorithm.
var geoTestCities = []struct {
	name                string
	longitude, latitude string
	score               string
}{
	{"Bangkok", "100.5252", "13.7220", "3962257306574459"},
	{"Beijing", "116.3972", "39.9075", "4069885364908765"},
	{"Berlin", "13.4105", "52.5244", "3673983964876493"},
	{"Copenhagen", "12.5655", "55.6759", "3685973395504349"},
	{"New Delhi", "77.2167", "28.6667", "3631527070936756"},
	{"Kathmandu", "85.3206", "27.7017", "3639507404773204"},
	{"London", "-0.1278", "51.5074", "2163557714755072"},
	{"New York", "-74.0060", "40.7128", "1791873974549446"},
	{"Paris", "2.3488", "48.8534", "3663832752681684"},
	{"Sydney", "151.2093", "-33.8688", "3252046221964352"},
	{"Tokyo", "139.6917", "35.6895", "4171231230197045"},
	{"Vienna", "16.3707", "48.2064", "3673109836391743"},
	{"Palermo", "13.361389", "38.115556", "3479099956230698"},
	{"Catania", "15.087269", "37.502669", "3479447370796909"},
	{"Eiffel Tower", "2.2944692", "48.8584625", "3663832614298053"},
}

func TestGeoAddScore(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "-0.127758", "51.507351", "London")
	expectReply(t, conn, ":1\r\n")

	// The score is the one Redis computes for these coordinates.
	do(t, conn, "ZSCORE", "places", "Paris")
	expectReply(t, conn, "$16\r\n3663832614298053\r\n")
}

func TestGeoAddScoreOfKnownLocations(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, city := range geoTestCities {
		do(t, conn, "GEOADD", "places", city.longitude, city.latitude, city.name)
		expectReply(t, conn, ":1\r\n")
	}
	for _, city := range geoTestCities {
		expectZScore(t, conn, "places", city.name, city.score)
	}

	// The set is ordered by score, like any sorted set.
	expectZRange(t, conn, "places", 0, -1,
		"New York", "London", "Sydney", "Palermo", "Catania", "New Delhi", "Kathmandu", "Eiffel Tower",
		"Paris", "Vienna", "Berlin", "Copenhagen", "Bangkok", "Beijing", "Tokyo")
}

func TestLocationScore(t *testing.T) {
	for _, city := range geoTestCities {
		longitude, _ := strconv.ParseFloat(city.longitude, 64)
		latitude, _ := strconv.ParseFloat(city.latitude, 64)
		want, _ := strconv.ParseFloat(city.score, 64)
		if got := (location{longitude: longitude, latitude: latitude}).score(); got != want {
			t.Errorf("score of %s (%s, %s) = %.0f, want %s", city.name, city.longitude, city.latitude, got, city.score)
		}
	}
}

func TestGeoAddScoreOfRemarkablePoints(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, tt := range []struct{ name, longitude, latitude, score string }{
		// The lowest coordinates there are have the lowest score there is.
		{"south-west corner", "-180", "-85.05112878", "0"},
		// The middle of both ranges is half of each of them: the leading
		// bit of the longitude and of the latitude, and nothing else.
		{"origin", "0", "0", "3377699720527872"},
		// Going east sets bits of the longitude, going north bits of the
		// latitude, which weigh half as much.
		{"west edge of the equator", "-180", "0", "1125899906842624"},
		{"south end of the prime meridian", "0", "-85.05112878", "2251799813685248"},
	} {
		do(t, conn, "GEOADD", "places", tt.longitude, tt.latitude, tt.name)
		expectReply(t, conn, ":1\r\n")
		expectZScore(t, conn, "places", tt.name, tt.score)
	}
}

func TestGeoAddScoreDependsOnTheCoordinatesOnly(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Neither the name of the location nor the key changes anything, and
	// neither does the way the numbers are written.
	tower := "tower-" + randomWord(t)
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", tower)
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", randomWord(t), "2.29446920", "+48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "others", "229446.92e-5", "0.488584625E2", "Paris")
	expectReply(t, conn, ":1\r\n")

	expectZScore(t, conn, "places", "Paris", "3663832614298053")
	expectZScore(t, conn, "places", tower, "3663832614298053")
	expectZScore(t, conn, "others", "Paris", "3663832614298053")
	// Two names at the same place have the same score: they are ordered by
	// name, and upper case comes first.
	expectZRange(t, conn, "places", 0, -1, "Paris", tower)
}

func TestGeoAddScoreFollowsTheLocation(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "somewhere")
	expectReply(t, conn, ":1\r\n")
	expectZScore(t, conn, "places", "somewhere", "3663832614298053")

	// A name that is given other coordinates is given their score.
	do(t, conn, "GEOADD", "places", "139.6917", "35.6895", "somewhere")
	expectReply(t, conn, ":0\r\n")
	expectZScore(t, conn, "places", "somewhere", "4171231230197045")
	do(t, conn, "GEOADD", "places", "-74.0060", "40.7128", "somewhere", "13.361389", "38.115556", "Palermo")
	expectReply(t, conn, ":1\r\n")
	expectZScore(t, conn, "places", "somewhere", "1791873974549446")
	expectZScore(t, conn, "places", "Palermo", "3479099956230698")
}

func TestGeoAddScoreIsTheSameAsZAdd(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Adding a location is adding a member with the score of the location.
	do(t, conn, "GEOADD", "places_key", "2.2944692", "48.8584625", "location")
	expectReply(t, conn, ":1\r\n")
	zadd(t, conn, "other_key", "3663832614298053", "location")

	expectSortedSet(t, srv, "places_key", scoredMember{"location", 3663832614298053})
	expectSortedSet(t, srv, "other_key", scoredMember{"location", 3663832614298053})
	// So much so that doing one after the other changes nothing.
	do(t, conn, "ZADD", "places_key", "3663832614298053", "location")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GEOADD", "other_key", "2.2944692", "48.8584625", "location")
	expectReply(t, conn, ":0\r\n")
}

func TestGeoAddScoreAfterARestart(t *testing.T) {
	args := []string{"--dir", t.TempDir(), "--appendonly", "yes", "--appendfsync", "always"}
	srv := newServerWithAOF(t, args...)
	conn := dial(t, startServer(t, srv))

	for _, city := range geoTestCities[:5] {
		do(t, conn, "GEOADD", "places", city.longitude, city.latitude, city.name)
		expectReply(t, conn, ":1\r\n")
	}

	// The scores are computed again from the coordinates that were logged.
	conn = dial(t, startServer(t, restartWithAOF(t, srv, args...)))
	for _, city := range geoTestCities[:5] {
		expectZScore(t, conn, "places", city.name, city.score)
	}
}
