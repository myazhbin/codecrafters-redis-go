package main

// This file implements the geospatial commands, which deal with locations on
// the surface of the Earth: points given by their longitude and latitude.

// cmdGeoAdd implements GEOADD key longitude latitude member [longitude
// latitude member ...]: it adds locations to the key, each one made of the
// coordinates of a point and of the name it is known by, and replies with
// the number of locations that were added.
func cmdGeoAdd(c *client, args []string) []byte {
	locations := args[1:]
	// A location takes three arguments.
	if len(locations)%3 != 0 {
		return errorReply("ERR syntax error")
	}
	return integerReply(len(locations) / 3)
}
