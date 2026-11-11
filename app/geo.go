package main

import (
	"math"
	"strconv"
)

// This file implements the geospatial commands, which deal with locations on
// the surface of the Earth: points given by their longitude and latitude.
//
// The locations of a key are kept in a sorted set, whose members are their
// names. The score of a member is computed from its coordinates in such a
// way that places close to one another tend to have scores that are close
// too, which is what makes it possible to look for the places of an area in a
// structure that is ordered along a single dimension.

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

// geoHashBits is the number of bits each coordinate of a location is given in
// its score.
const geoHashBits = 26

// score returns the score that stands for the location in a sorted set.
//
// Each coordinate is first turned into an integer of geoHashBits bits, which
// tells where it is in the range of values it can have: cut that range in
// two halves and the first bit says which half the coordinate is in, the
// second bit which half of that half, and so on. The bits of the two integers
// are then interleaved, starting with the longitude's: the more leading bits
// two scores have in common, the smaller the area both locations are in.
func (l location) score() float64 {
	latitude := (l.latitude - minLatitude) / (maxLatitude - minLatitude)
	longitude := (l.longitude - minLongitude) / (maxLongitude - minLongitude)
	latitude *= 1 << geoHashBits
	longitude *= 1 << geoHashBits
	return float64(spreadBits(uint32(latitude)) | spreadBits(uint32(longitude))<<1)
}

// spreadBits returns x with a zero bit inserted before each of its bits: bit
// 0 stays where it is, bit 1 becomes bit 2, bit 2 becomes bit 4, and so on.
func spreadBits(x uint32) uint64 {
	v := uint64(x)
	v = (v | v<<16) & 0x0000FFFF0000FFFF
	v = (v | v<<8) & 0x00FF00FF00FF00FF
	v = (v | v<<4) & 0x0F0F0F0F0F0F0F0F
	v = (v | v<<2) & 0x3333333333333333
	v = (v | v<<1) & 0x5555555555555555
	return v
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
// latitude member ...]: it adds locations to the sorted set stored at key,
// creating it if needed. Each location is made of the coordinates of a point
// and of the name it is known by, which is its member in the set. A name that
// is already in the set is given its new coordinates.
//
// The reply is the number of locations that were added: the ones that only
// changed place don't count.
//
// The coordinates of every location are checked before any of them is added:
// the command is applied in full or not at all.
func cmdGeoAdd(c *client, args []string) []byte {
	s := c.server
	key, triples := args[0], args[1:]
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

	z, err := s.store.getOrCreateSortedSet(key)
	if err != nil {
		return errorReply(err.Error())
	}
	added, modified := 0, false
	for i, l := range locations {
		wasAdded, changed := z.add(triples[3*i+2], l.score())
		if wasAdded {
			added++
		}
		modified = modified || changed
	}
	if modified {
		s.store.modified(key)
		s.propagate(append([]string{"GEOADD"}, args...)...)
	}
	return integerReply(added)
}
