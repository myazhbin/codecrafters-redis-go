package main

import (
	"math"
	"strconv"
)

// This file implements the geospatial commands, which deal with locations on
// the surface of the Earth: points given by their longitude and latitude.

// The coordinates a location can have, in degrees. Longitudes go all the way
// around the Earth. Latitudes stop short of the poles, which are at 90
// degrees: these are the limits of the Web Mercator projection, in which the
// part of the world that is represented is a square.
const (
	minLongitude, maxLongitude = -180.0, 180.0
	minLatitude, maxLatitude   = -85.05112878, 85.05112878
)

// location is a point on the surface of the Earth.
type location struct {
	longitude, latitude float64
}

// parseLocation parses the coordinates of a location, given in degrees as
// decimal numbers. On invalid input it returns the error reply to send
// instead: coordinates have to be numbers, within the limits above.
func parseLocation(longitude, latitude string) (location, []byte) {
	lon, lonOK := parseScore(longitude)
	lat, latOK := parseScore(latitude)
	if !lonOK || !latOK {
		return location{}, errorReply("ERR value is not a valid float")
	}
	if lon < minLongitude || lon > maxLongitude || lat < minLatitude || lat > maxLatitude {
		return location{}, errorReply("ERR invalid longitude,latitude pair " + fixedPoint(lon) + "," + fixedPoint(lat))
	}
	return location{longitude: lon, latitude: lat}, nil
}

// fixedPoint writes a number with six digits after the decimal point, as it
// appears in error messages.
func fixedPoint(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return strconv.FormatFloat(f, 'f', 6, 64)
}

// cmdGeoAdd implements GEOADD key longitude latitude member [longitude
// latitude member ...]: it adds locations to the key, each one made of the
// coordinates of a point and of the name it is known by, and replies with
// the number of locations that were added.
//
// The coordinates of every location are checked before any of them is added:
// the command is applied in full or not at all.
func cmdGeoAdd(c *client, args []string) []byte {
	triples := args[1:]
	// A location takes three arguments.
	if len(triples)%3 != 0 {
		return errorReply("ERR syntax error")
	}

	locations := make([]location, len(triples)/3)
	for i := range locations {
		var errReply []byte
		locations[i], errReply = parseLocation(triples[3*i], triples[3*i+1])
		if errReply != nil {
			return errReply
		}
	}
	return integerReply(len(locations))
}
