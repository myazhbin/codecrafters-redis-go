package main

import (
	"math"
	"strconv"
	"strings"
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

// squashBits is the reverse of spreadBits: it returns the number made of the
// bits of v that are at even positions, bit 0 first.
func squashBits(v uint64) uint32 {
	v &= 0x5555555555555555
	v = (v | v>>1) & 0x3333333333333333
	v = (v | v>>2) & 0x0F0F0F0F0F0F0F0F
	v = (v | v>>4) & 0x00FF00FF00FF00FF
	v = (v | v>>8) & 0x0000FFFF0000FFFF
	v = (v | v>>16) & 0x00000000FFFFFFFF
	return uint32(v)
}

// locationOfScore returns the location a score stands for.
//
// A score doesn't have the precision of the coordinates it was computed
// from: all the points of a small area, less than a meter across, have the
// same one. The location that is returned is the center of that area, which
// is very close to the one that was given, but rarely the very same.
func locationOfScore(score float64) location {
	// The scores of locations are whole numbers of 52 bits. A sorted set can
	// hold members with any score, which are decoded as best as can be.
	var bits uint64
	switch {
	case score >= 1<<64:
		bits = math.MaxUint64
	case score > 0:
		bits = uint64(score)
	}
	latitude, longitude := squashBits(bits), squashBits(bits>>1)

	// The area goes from the point the two integers stand for to the point
	// of the next integers.
	//
	// Each bound is a product added to a minimum. Redis computes that in one
	// step, rounding once, on the processors that can; rounding the product
	// first sometimes gives a result that differs by its last digit. math.FMA
	// does it in one step on every processor, which is also what the
	// reference values of this computation were obtained with.
	const steps = 1 << geoHashBits
	latitudeRange, longitudeRange := maxLatitude-minLatitude, maxLongitude-minLongitude
	south := math.FMA(float64(latitude)/steps, latitudeRange, minLatitude)
	north := math.FMA(float64(latitude+1)/steps, latitudeRange, minLatitude)
	west := math.FMA(float64(longitude)/steps, longitudeRange, minLongitude)
	east := math.FMA(float64(longitude+1)/steps, longitudeRange, minLongitude)

	return location{
		longitude: min(max((west+east)/2, minLongitude), maxLongitude),
		latitude:  min(max((south+north)/2, minLatitude), maxLatitude),
	}
}

// earthRadius is the radius of the Earth, in meters, as Redis has it. The
// Earth is taken to be a perfect sphere.
const earthRadius = 6372797.560856

// radians converts an angle from degrees to radians.
func radians(degrees float64) float64 {
	return degrees * (math.Pi / 180)
}

// distanceTo returns the distance between two locations, in meters: the
// length of the shortest path from one to the other on the surface of the
// Earth, which is an arc of a great circle. It is computed with the haversine
// formula.
func (l location) distanceTo(other location) float64 {
	v := math.Sin((radians(other.longitude) - radians(l.longitude)) / 2)
	// Two locations that have the same longitude are on the same meridian:
	// the distance is the difference between their latitudes, as an angle,
	// times the radius.
	if v == 0 {
		return earthRadius * math.Abs(radians(other.latitude)-radians(l.latitude))
	}

	lat1, lat2 := radians(l.latitude), radians(other.latitude)
	u := math.Sin((lat2 - lat1) / 2)
	// The conversions to float64 keep every step of the computation rounded
	// the same way on every kind of processor.
	a := float64(u*u) + float64(math.Cos(lat1)*math.Cos(lat2)*v*v)
	return 2 * earthRadius * math.Asin(math.Sqrt(a))
}

// geoUnits are the units a distance can be given or asked for in, by name,
// along with how many meters they are worth.
var geoUnits = map[string]float64{
	"m":  1,
	"km": 1000,
	"ft": 0.3048,
	"mi": 1609.34,
}

// parseGeoUnit returns how many meters the unit of the given name is worth.
// Names are case-insensitive. On invalid input it returns the error reply to
// send instead.
func parseGeoUnit(name string) (float64, []byte) {
	meters, ok := geoUnits[strings.ToLower(name)]
	if !ok {
		return 0, errorReply("ERR unsupported unit provided. please use M, KM, FT, MI")
	}
	return meters, nil
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

// cmdGeoPos implements GEOPOS key [member ...]: it replies with the position
// of each of the members in the set of locations stored at key, in the order
// they are given, as an array made of its longitude and its latitude, both
// written as bulk strings. A member that isn't in the set gets a null array
// instead, which is what every member gets when the key doesn't exist.
//
// The coordinates are the ones the score of the member stands for: see
// locationOfScore for how they compare with the ones the location was added
// with.
func cmdGeoPos(c *client, args []string) []byte {
	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}

	positions := make([][]byte, len(args)-1)
	for i, member := range args[1:] {
		positions[i] = nullArray
		if z == nil {
			continue
		}
		if score, ok := z.scores[member]; ok {
			l := locationOfScore(score)
			positions[i] = bulkStringArray([]string{formatScore(l.longitude), formatScore(l.latitude)})
		}
	}
	return arrayReply(positions...)
}

// cmdGeoDist implements GEODIST key member1 member2 [M | KM | FT | MI]: it
// replies with the distance between the two members of the set of locations
// stored at key, as a bulk string that holds a number with four digits after
// the decimal point. The distance is in meters, unless another unit is asked
// for: kilometers, feet or miles.
//
// The reply is a null bulk string if one of the members isn't in the set, or
// if the key doesn't exist.
func cmdGeoDist(c *client, args []string) []byte {
	if len(args) > 4 {
		return errorReply("ERR syntax error")
	}
	unit := 1.0
	if len(args) == 4 {
		var errReply []byte
		if unit, errReply = parseGeoUnit(args[3]); errReply != nil {
			return errReply
		}
	}

	z, err := c.server.store.getSortedSet(args[0])
	if err != nil {
		return errorReply(err.Error())
	}
	if z == nil {
		return nullBulkString
	}
	from, fromOK := z.scores[args[1]]
	to, toOK := z.scores[args[2]]
	if !fromOK || !toOK {
		return nullBulkString
	}

	// The distance is the one between the positions the members are known
	// to be at, which are those their scores stand for.
	meters := locationOfScore(from).distanceTo(locationOfScore(to))
	return bulkString(strconv.FormatFloat(meters/unit, 'f', 4, 64))
}
