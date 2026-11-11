package main

import (
	"strings"
	"testing"
)

func TestGeoAddInvalidLatitude(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "180", "90", "test1")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 180.000000,90.000000\r\n")
}

func TestGeoAddInvalidLongitude(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "181", "0.3", "test2")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 181.000000,0.300000\r\n")
}

func TestGeoAddInvalidLongitudeAndLatitude(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "location_key", "200", "100", "foo")
	// The error is a RESP simple error that starts with ERR, and names what
	// is wrong.
	line := readReplyLine(t, conn)
	if !strings.HasPrefix(line, "-ERR ") || !strings.Contains(line, "longitude") || !strings.Contains(line, "latitude") {
		t.Errorf("reply = %q, want an error that starts with ERR and mentions the longitude and the latitude", line)
	}
	if want := "-ERR invalid longitude,latitude pair 200.000000,100.000000"; line != want {
		t.Errorf("reply = %q, want %q", line, want)
	}
}

func TestGeoAddCoordinatesOutOfRange(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, tt := range []struct{ longitude, latitude, pair string }{
		// The longitude is just out of range, on either side.
		{"180.000001", "0", "180.000001,0.000000"},
		{"-180.000001", "0", "-180.000001,0.000000"},
		{"360", "48.8584625", "360.000000,48.858463"},
		{"-200.5", "-10", "-200.500000,-10.000000"},
		// So is the latitude, which stops before the poles.
		{"0", "85.05112879", "0.000000,85.051129"},
		{"0", "-85.05112879", "0.000000,-85.051129"},
		{"2.2944692", "86", "2.294469,86.000000"},
		{"2.2944692", "-90", "2.294469,-90.000000"},
		{"0", "1e10", "0.000000,10000000000.000000"},
		// Infinity is a number, but not a place.
		{"inf", "0", "inf,0.000000"},
		{"0", "-inf", "0.000000,-inf"},
	} {
		do(t, conn, "GEOADD", "places", tt.longitude, tt.latitude, "somewhere")
		expectReply(t, conn, "-ERR invalid longitude,latitude pair "+tt.pair+"\r\n")
	}
}

func TestGeoAddCoordinatesAtTheLimits(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The limits themselves are valid.
	for i, coordinates := range [][2]string{
		{"180", "0"}, {"-180", "0"}, {"0", "85.05112878"}, {"0", "-85.05112878"},
		{"180", "85.05112878"}, {"-180", "-85.05112878"}, {"180", "-85.05112878"}, {"-180", "85.05112878"},
		{"180.0", "85.051128780"}, {"1.8e2", "8.505112878e1"},
		{"0", "0"}, {"-0", "-0"}, {"179.999999", "85.05112877"},
	} {
		do(t, conn, "GEOADD", "places", coordinates[0], coordinates[1], "limit-"+string(rune('a'+i)))
		expectReply(t, conn, ":1\r\n")
	}
}

func TestGeoAddCoordinatesThatAreNotNumbers(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, coordinates := range [][2]string{
		{"abc", "48.164271"}, {"11.5030378", "xyz"}, {"", "48.164271"}, {"11.5030378", ""},
		{"11,5", "48.164271"}, {"11.5030378", "48.16.42"}, {" 11.5", "48.164271"}, {"11.5030378", "48.164271 "},
		{"nan", "48.164271"}, {"11.5030378", "NaN"}, {"11.5E", "48.164271"}, {"east", "north"},
		// A coordinate that isn't a number is reported before one that is
		// out of range.
		{"abc", "100"}, {"200", "xyz"},
	} {
		do(t, conn, "GEOADD", "places", coordinates[0], coordinates[1], "somewhere")
		expectReply(t, conn, "-ERR value is not a valid float\r\n")
	}
}

func TestGeoAddChecksEveryLocation(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// One invalid location, wherever it is, and the command fails.
	do(t, conn, "GEOADD", "places", "200", "100", "nowhere", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "11.5030378", "95", "nowhere")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 11.503038,95.000000\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris", "11.5030378", "north", "nowhere", "-0.127758", "51.507351", "London")
	expectReply(t, conn, "-ERR value is not a valid float\r\n")
	// The first one that is wrong is the one that is reported.
	do(t, conn, "GEOADD", "places", "2.2944692", "100", "first", "200", "48.8584625", "second")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 2.294469,100.000000\r\n")

	// The arguments have to come in threes before they are looked at.
	do(t, conn, "GEOADD", "places", "200", "100", "nowhere", "extra")
	expectReply(t, conn, "-ERR syntax error\r\n")
}

func TestGeoAddAfterAnInvalidLocation(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "200", "100", "foo")
	expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
	// The connection is fine, and so is the key.
	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
}

func TestGeoAddValidationInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GEOADD", "places", "11.5030378", "48.164271", "Munich"},
		{"GEOADD", "places", "181", "0.3", "test2"},
		{"GEOADD", "places", "east", "0.3", "test3"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// The coordinates are checked when the command is executed.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n", "-ERR invalid longitude,latitude pair 181.000000,0.300000\r\n", "-ERR value is not a valid float\r\n"))
}
