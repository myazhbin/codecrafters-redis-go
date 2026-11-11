package main

import (
	"testing"
)

func TestGeoAdd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply is the number of locations added, as a RESP integer.
	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
}

func TestGeoAddOfSeveralPlaces(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	// Another key is another set of locations.
	do(t, conn, "GEOADD", "landmarks", "-74.0445", "40.6892", "Statue of Liberty")
	expectReply(t, conn, ":1\r\n")

	// Neither the key nor the name of the location can be hardcoded.
	for range 3 {
		do(t, conn, "GEOADD", randomWord(t), "13.361389", "38.115556", randomWord(t))
		expectReply(t, conn, ":1\r\n")
	}
}

func TestGeoAddSeveralLocationsAtOnce(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "GEOADD", "Sicily", "13.361389", "38.115556", "Palermo", "15.087269", "37.502669", "Catania")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "GEOADD", "cities", "11.5030378", "48.164271", "Munich", "2.2944692", "48.8584625", "Paris", "-0.127758", "51.507351", "London")
	expectReply(t, conn, ":3\r\n")
}

func TestGeoAddWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{
		{"GEOADD"},
		{"GEOADD", "places"},
		{"GEOADD", "places", "11.5030378"},
		{"GEOADD", "places", "11.5030378", "48.164271"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'geoadd' command\r\n")
	}

	// A location is a longitude, a latitude and a name: nothing less.
	for _, extra := range [][]string{{"2.2944692"}, {"2.2944692", "48.8584625"}, {"2.2944692", "48.8584625", "Paris", "0"}} {
		do(t, conn, append([]string{"GEOADD", "places", "11.5030378", "48.164271", "Munich"}, extra...)...)
		expectReply(t, conn, "-ERR syntax error\r\n")
	}
}

func TestGeoAddCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"GEOADD", "geoadd", "GeoAdd"} {
		do(t, conn, name, "places-of-"+name, "11.5030378", "48.164271", "Munich")
		expectReply(t, conn, ":1\r\n")
	}
}
