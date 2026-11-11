package main

import (
	"math"
	"math/rand"
	"net"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"
)

// geoSearch sends GEOSEARCH on the key with the given options, and returns
// the members of its reply, in the order they come.
func geoSearch(t *testing.T, conn net.Conn, key string, options ...string) []string {
	t.Helper()

	do(t, conn, append([]string{"GEOSEARCH", key}, options...)...)
	header := readReplyLine(t, conn)
	if len(header) < 2 || header[0] != '*' {
		t.Fatalf("reply to GEOSEARCH %q starts with %q, want an array", options, header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil || count < 0 {
		t.Fatalf("reply to GEOSEARCH %q starts with %q, want an array", options, header)
	}
	members := make([]string, count)
	for i := range members {
		members[i] = readBulkString(t, conn)
	}
	return members
}

// expectGeoSearch checks that GEOSEARCH finds exactly the given members, in
// any order.
func expectGeoSearch(t *testing.T, conn net.Conn, key string, options []string, want ...string) {
	t.Helper()

	got := geoSearch(t, conn, key, options...)
	sort.Strings(got)
	want = append([]string{}, want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GEOSEARCH %s %q found %q, want %q", key, options, got, want)
	}
}

// expectGeoSearchInOrder checks that GEOSEARCH finds exactly the given
// members, in that order.
func expectGeoSearchInOrder(t *testing.T, conn net.Conn, key string, options []string, want ...string) {
	t.Helper()

	if got := geoSearch(t, conn, key, options...); !reflect.DeepEqual(got, append([]string{}, want...)) {
		t.Errorf("GEOSEARCH %s %q found %q, want %q in that order", key, options, got, want)
	}
}

// addGeoSearchCities stores the three cities the searches of the tests are
// made among.
func addGeoSearchCities(t *testing.T, conn net.Conn) {
	t.Helper()

	do(t, conn, "GEOADD", "places", "11.5030378", "48.164271", "Munich")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "2.2944692", "48.8584625", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "GEOADD", "places", "-0.0884948", "51.506479", "London")
	expectReply(t, conn, ":1\r\n")
}

func TestGeoSearch(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	// The reply is an array of the names of the locations that are in the
	// circle, as bulk strings.
	do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100000", "m")
	expectReply(t, conn, "*1\r\n$5\r\nParis\r\n")

	expectGeoSearch(t, conn, "places", []string{"FROMLONLAT", "2", "48", "BYRADIUS", "500000", "m"}, "Paris", "London")
	expectGeoSearch(t, conn, "places", []string{"FROMLONLAT", "11", "50", "BYRADIUS", "300000", "m"}, "Munich")
}

func TestGeoSearchFindsNothing(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	// Paris is 98 kilometers away from this point, not 100 meters.
	do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "m")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "-150", "-40", "BYRADIUS", "1000", "km")
	expectReply(t, conn, "*0\r\n")

	// A key that doesn't exist has no location to find.
	do(t, conn, "GEOSEARCH", "missing_key", "FROMLONLAT", "2", "48", "BYRADIUS", "500000", "m")
	expectReply(t, conn, "*0\r\n")
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("places"))
}

func TestGeoSearchGrowsWithTheRadius(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	// From the Eiffel tower: Paris is there, London 340 kilometers away and
	// Munich 682.
	for _, tt := range []struct {
		radius string
		want   []string
	}{
		{"0", nil},
		{"1", []string{"Paris"}},
		{"339000", []string{"Paris"}},
		{"341000", []string{"Paris", "London"}},
		{"682000", []string{"Paris", "London"}},
		{"683000", []string{"Paris", "London", "Munich"}},
		{"20100000", []string{"Paris", "London", "Munich"}},
		{"inf", []string{"Paris", "London", "Munich"}},
	} {
		expectGeoSearch(t, conn, "places", []string{"FROMLONLAT", "2.2944692", "48.8584625", "BYRADIUS", tt.radius, "m"}, tt.want...)
	}
}

func TestGeoSearchUnits(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	from := []string{"FROMLONLAT", "2.2944692", "48.8584625"}
	search := func(radius, unit string, want ...string) {
		t.Helper()
		expectGeoSearch(t, conn, "places", append(append([]string{}, from...), "BYRADIUS", radius, unit), want...)
	}

	// London is 339.9 kilometers away, which is 211.2 miles or 1,115,115
	// feet.
	search("341", "km", "Paris", "London")
	search("339", "km", "Paris")
	search("340.5", "KM", "Paris", "London")
	search("212", "mi", "Paris", "London")
	search("211", "mi", "Paris")
	search("212", "Mi", "Paris", "London")
	search("1116000", "ft", "Paris", "London")
	search("1115000", "FT", "Paris")
	search("341000", "M", "Paris", "London")
	search("3.41e5", "m", "Paris", "London")
}

func TestGeoSearchOptionNamesAreCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	for _, names := range [][3]string{
		{"GEOSEARCH", "FROMLONLAT", "BYRADIUS"},
		{"geosearch", "fromlonlat", "byradius"},
		{"GeoSearch", "FromLonLat", "ByRadius"},
	} {
		do(t, conn, names[0], "places", names[1], "2", "48", names[2], "100000", "m")
		expectReply(t, conn, bulkArray("Paris"))
	}
	// The options can come in any order.
	expectGeoSearch(t, conn, "places", []string{"BYRADIUS", "100", "km", "FROMLONLAT", "2", "48"}, "Paris")
	expectGeoSearchInOrder(t, conn, "places", []string{"DESC", "BYRADIUS", "500", "km", "FROMLONLAT", "2", "48"}, "London", "Paris")
}

func TestGeoSearchFromAMember(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	// The circle is centered on where the member is: the member is in it,
	// whatever the radius.
	expectGeoSearch(t, conn, "places", []string{"FROMMEMBER", "Paris", "BYRADIUS", "0", "m"}, "Paris")
	expectGeoSearch(t, conn, "places", []string{"FROMMEMBER", "Paris", "BYRADIUS", "343", "km"}, "Paris", "London")
	expectGeoSearch(t, conn, "places", []string{"FROMMEMBER", "Munich", "BYRADIUS", "683", "km"}, "Munich", "Paris")
	expectGeoSearch(t, conn, "places", []string{"frommember", "London", "BYRADIUS", "1000", "km"}, "London", "Paris", "Munich")

	// A member that isn't in the set is nowhere.
	do(t, conn, "GEOSEARCH", "places", "FROMMEMBER", "Atlantis", "BYRADIUS", "100", "km")
	expectReply(t, conn, "-ERR could not decode requested zset member\r\n")
	// Unless the set doesn't exist either: there is nothing to find then.
	do(t, conn, "GEOSEARCH", "missing_key", "FROMMEMBER", "Atlantis", "BYRADIUS", "100", "km")
	expectReply(t, conn, "*0\r\n")
}

func TestGeoSearchSortedByDistance(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoTestCities(t, conn, "cities")

	// From Berlin, nearest first: Copenhagen is 355 kilometers away, Vienna
	// 524, the center of Paris 879, its tower 882 and London 932. Palermo,
	// at 1,603 kilometers, is out of reach.
	from := []string{"FROMMEMBER", "Berlin", "BYRADIUS", "1500", "km"}
	nearestFirst := []string{"Berlin", "Copenhagen", "Vienna", "Paris", "Eiffel Tower", "London"}
	expectGeoSearchInOrder(t, conn, "cities", append(append([]string{}, from...), "ASC"), nearestFirst...)
	expectGeoSearchInOrder(t, conn, "cities", append(append([]string{}, from...), "asc"), nearestFirst...)

	farthestFirst := []string{"London", "Eiffel Tower", "Paris", "Vienna", "Copenhagen", "Berlin"}
	expectGeoSearchInOrder(t, conn, "cities", append(append([]string{}, from...), "DESC"), farthestFirst...)
	// The last one that is given wins.
	expectGeoSearchInOrder(t, conn, "cities", append(append([]string{}, from...), "ASC", "DESC"), farthestFirst...)

	// Without ASC or DESC the same members are found, in an order that
	// isn't specified.
	expectGeoSearch(t, conn, "cities", from, nearestFirst...)
}

func TestGeoSearchCount(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoTestCities(t, conn, "cities")

	from := []string{"FROMMEMBER", "Berlin", "BYRADIUS", "1500", "km"}
	with := func(options ...string) []string { return append(append([]string{}, from...), options...) }

	// A limited number of members are the nearest ones, nearest first.
	expectGeoSearchInOrder(t, conn, "cities", with("COUNT", "3"), "Berlin", "Copenhagen", "Vienna")
	expectGeoSearchInOrder(t, conn, "cities", with("COUNT", "1"), "Berlin")
	expectGeoSearchInOrder(t, conn, "cities", with("COUNT", "3", "ASC"), "Berlin", "Copenhagen", "Vienna")
	expectGeoSearchInOrder(t, conn, "cities", with("count", "2", "asc"), "Berlin", "Copenhagen")
	// Or the farthest ones, when the farthest come first.
	expectGeoSearchInOrder(t, conn, "cities", with("COUNT", "3", "DESC"), "London", "Eiffel Tower", "Paris")
	expectGeoSearchInOrder(t, conn, "cities", with("DESC", "COUNT", "2"), "London", "Eiffel Tower")
	// A count that is higher than the number of members found changes
	// nothing.
	expectGeoSearchInOrder(t, conn, "cities", with("COUNT", "100"), "Berlin", "Copenhagen", "Vienna", "Paris", "Eiffel Tower", "London")
}

func TestGeoSearchCountAny(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoTestCities(t, conn, "cities")

	inRange := map[string]bool{"Berlin": true, "Copenhagen": true, "Vienna": true, "Eiffel Tower": true, "Paris": true, "London": true}
	from := []string{"FROMMEMBER", "Berlin", "BYRADIUS", "1500", "km"}

	// With ANY, the members are the first ones that are found: as many as
	// asked for, and all of them in the circle.
	for _, options := range [][]string{{"COUNT", "3", "ANY"}, {"ANY", "COUNT", "3"}, {"COUNT", "3", "any", "ASC"}} {
		got := geoSearch(t, conn, "cities", append(append([]string{}, from...), options...)...)
		if len(got) != 3 {
			t.Errorf("GEOSEARCH %q found %d members, want 3", options, len(got))
		}
		seen := map[string]bool{}
		for _, member := range got {
			if !inRange[member] || seen[member] {
				t.Errorf("GEOSEARCH %q found %q, want distinct members of the circle", options, got)
			}
			seen[member] = true
		}
	}
	expectGeoSearch(t, conn, "cities", append(append([]string{}, from...), "COUNT", "100", "ANY"),
		"Berlin", "Copenhagen", "Vienna", "Eiffel Tower", "Paris", "London")

	do(t, conn, append([]string{"GEOSEARCH", "cities"}, append(append([]string{}, from...), "ANY")...)...)
	expectReply(t, conn, "-ERR the ANY argument requires COUNT argument\r\n")
}

func TestGeoSearchAroundTheWorld(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoTestCities(t, conn, "cities")

	all := make([]string, len(geoTestCities))
	for i, city := range geoTestCities {
		all[i] = city.name
	}
	// No two places are farther apart than half the circumference of the
	// Earth, which is 20,020 kilometers.
	expectGeoSearch(t, conn, "cities", []string{"FROMLONLAT", "0", "0", "BYRADIUS", "20100", "km"}, all...)
	expectGeoSearch(t, conn, "cities", []string{"FROMLONLAT", "-120", "-60", "BYRADIUS", "20100", "km"}, all...)

	// A circle can go over the line where the longitude jumps from 180 to
	// -180 degrees.
	do(t, conn, "GEOADD", "pacific", "179.5", "-16.5", "Fiji", "-179.2", "-8.5", "Tuvalu", "-175.2", "-21.1", "Tonga", "-157.8", "21.3", "Honolulu")
	expectReply(t, conn, ":4\r\n")
	expectGeoSearch(t, conn, "pacific", []string{"FROMLONLAT", "180", "-15", "BYRADIUS", "1000", "km"}, "Fiji", "Tuvalu", "Tonga")
	expectGeoSearch(t, conn, "pacific", []string{"FROMLONLAT", "-180", "-15", "BYRADIUS", "1000", "km"}, "Fiji", "Tuvalu", "Tonga")
	expectGeoSearchInOrder(t, conn, "pacific", []string{"FROMMEMBER", "Tuvalu", "BYRADIUS", "1500", "km", "ASC"}, "Tuvalu", "Fiji", "Tonga")
}

func TestGeoSearchMatchesModel(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Locations spread all over the world, and where the server has them.
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	type position struct{ longitude, latitude float64 }
	positions := make(map[string]position)
	for i := range 300 {
		name := "place-" + strconv.Itoa(i)
		longitude := strconv.FormatFloat(-180+360*rng.Float64(), 'f', 6, 64)
		latitude := strconv.FormatFloat(-85+170*rng.Float64(), 'f', 6, 64)
		do(t, conn, "GEOADD", "world", longitude, latitude, name)
		expectReply(t, conn, ":1\r\n")
		p := geoPos(t, conn, "world", name)[0]
		lon, _ := strconv.ParseFloat(p[0], 64)
		lat, _ := strconv.ParseFloat(p[1], 64)
		positions[name] = position{lon, lat}
	}

	// Searches from random points are compared with the distances computed
	// by another implementation of the formula.
	for range 40 {
		longitude, latitude := -180+360*rng.Float64(), -85+170*rng.Float64()
		radius := 100000 + 9000000*rng.Float64()

		var want []string
		ambiguous := false
		for name, p := range positions {
			distance := haversine(longitude, latitude, p.longitude, p.latitude)
			// A place that is on the circle, to the centimeter, could be on
			// either side of it.
			if math.Abs(distance-radius) < 0.01 {
				ambiguous = true
			}
			if distance <= radius {
				want = append(want, name)
			}
		}
		if ambiguous {
			continue
		}
		expectGeoSearch(t, conn, "world", []string{
			"FROMLONLAT", strconv.FormatFloat(longitude, 'f', -1, 64), strconv.FormatFloat(latitude, 'f', -1, 64),
			"BYRADIUS", strconv.FormatFloat(radius, 'f', -1, 64), "m",
		}, want...)
	}
}

func TestGeoSearchAmongMembersAddedWithZAdd(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A location is its score, however it got it.
	for _, city := range geoTestCities {
		zadd(t, conn, "cities", city.score, city.name)
	}
	expectGeoSearch(t, conn, "cities", []string{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"}, "Paris", "Eiffel Tower")
	expectGeoSearchInOrder(t, conn, "cities", []string{"FROMMEMBER", "Palermo", "BYRADIUS", "200", "km", "ASC"}, "Palermo", "Catania")
}

func TestGeoSearchFollowsTheLocations(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)
	search := []string{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"}

	expectGeoSearch(t, conn, "places", search, "Paris")
	// A member that moves into the circle is found, one that moves out of
	// it or is removed isn't anymore.
	do(t, conn, "GEOADD", "places", "2.1", "48.1", "Munich")
	expectReply(t, conn, ":0\r\n")
	expectGeoSearch(t, conn, "places", search, "Paris", "Munich")
	do(t, conn, "GEOADD", "places", "100", "0", "Paris")
	expectReply(t, conn, ":0\r\n")
	expectGeoSearch(t, conn, "places", search, "Munich")
	do(t, conn, "ZREM", "places", "Munich")
	expectReply(t, conn, ":1\r\n")
	expectGeoSearch(t, conn, "places", search)
}

func TestGeoSearchInvalidCenter(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	// The center is checked whether the key exists or not.
	for _, key := range []string{"places", "missing_key"} {
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "200", "100", "BYRADIUS", "100", "km")
		expectReply(t, conn, "-ERR invalid longitude,latitude pair 200.000000,100.000000\r\n")
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "86", "BYRADIUS", "100", "km")
		expectReply(t, conn, "-ERR invalid longitude,latitude pair 2.000000,86.000000\r\n")
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "east", "48", "BYRADIUS", "100", "km")
		expectReply(t, conn, "-ERR value is not a valid float\r\n")
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "", "BYRADIUS", "100", "km")
		expectReply(t, conn, "-ERR value is not a valid float\r\n")
	}
}

func TestGeoSearchInvalidRadius(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	for _, key := range []string{"places", "missing_key"} {
		for _, radius := range []string{"far", "", "1,5", "nan", "10km"} {
			do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "48", "BYRADIUS", radius, "km")
			expectReply(t, conn, "-ERR need numeric radius\r\n")
		}
		for _, radius := range []string{"-1", "-0.001", "-inf"} {
			do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "48", "BYRADIUS", radius, "km")
			expectReply(t, conn, "-ERR radius cannot be negative\r\n")
		}
		for _, unit := range []string{"meters", "kilometers", "", "cm", "yd", "100"} {
			do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "48", "BYRADIUS", "100", unit)
			expectReply(t, conn, "-ERR unsupported unit provided. please use M, KM, FT, MI\r\n")
		}
	}
}

func TestGeoSearchInvalidCount(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	for _, count := range []string{"0", "-1", "-100"} {
		do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "COUNT", count)
		expectReply(t, conn, "-ERR COUNT must be > 0\r\n")
	}
	for _, count := range []string{"many", "", "1.5", "+1", "9223372036854775808"} {
		do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "COUNT", count)
		expectReply(t, conn, "-ERR value is not an integer or out of range\r\n")
	}
	// A count as high as an integer can be is fine.
	expectGeoSearch(t, conn, "places", []string{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "COUNT", "9223372036854775807"}, "Paris")
}

func TestGeoSearchInvalidOptions(t *testing.T) {
	conn := dial(t, startTestServer(t))
	addGeoSearchCities(t, conn)

	for _, command := range [][]string{
		{"GEOSEARCH"},
		{"GEOSEARCH", "places"},
		{"GEOSEARCH", "places", "FROMLONLAT", "2", "48"},
		{"GEOSEARCH", "places", "FROMMEMBER", "Paris", "BYRADIUS", "100"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'geosearch' command\r\n")
	}

	// A search has a center and a radius: one of each.
	do(t, conn, "GEOSEARCH", "places", "BYRADIUS", "100", "km", "ASC", "COUNT", "1", "ANY")
	expectReply(t, conn, "-ERR exactly one of FROMMEMBER or FROMLONLAT can be specified for GEOSEARCH\r\n")
	do(t, conn, "GEOSEARCH", "places", "FROMLONLAT", "2", "48", "ASC", "COUNT", "1", "ANY")
	expectReply(t, conn, "-ERR exactly one of BYRADIUS and BYBOX can be specified for GEOSEARCH\r\n")

	for _, options := range [][]string{
		// Two centers, or two radiuses.
		{"FROMLONLAT", "2", "48", "FROMMEMBER", "Paris", "BYRADIUS", "100", "km"},
		{"FROMMEMBER", "Paris", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"},
		{"FROMLONLAT", "2", "48", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "BYRADIUS", "200", "km"},
		// An option that is cut short.
		{"BYRADIUS", "100", "km", "ASC", "DESC", "FROMLONLAT", "2"},
		{"FROMLONLAT", "2", "48", "ASC", "DESC", "BYRADIUS", "100"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "COUNT"},
		{"BYRADIUS", "100", "km", "ASC", "DESC", "ASC", "FROMMEMBER"},
		// Something that isn't an option.
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "NEAREST"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "5"},
		{"AROUND", "2", "48", "BYRADIUS", "100", "km"},
		// The options of Redis that aren't supported.
		{"FROMLONLAT", "2", "48", "BYBOX", "100", "100", "km"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "WITHDIST"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "WITHCOORD"},
		{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km", "WITHHASH"},
	} {
		do(t, conn, append([]string{"GEOSEARCH", "places"}, options...)...)
		expectReply(t, conn, "-ERR syntax error\r\n")
	}
}

func TestGeoSearchOnAKeyOfAnotherType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	setString(t, conn, "string_key", "Paris")
	do(t, conn, "RPUSH", "list_key", "Paris")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "Paris", "v")
	expectReply(t, conn, bulk("1-1"))

	for _, key := range []string{"string_key", "list_key", "stream_key"} {
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km")
		expectReply(t, conn, wrongTypeError)
		// The type of the key is the first thing that is checked.
		do(t, conn, "GEOSEARCH", key, "FROMLONLAT", "200", "100", "BYRADIUS", "far", "away")
		expectReply(t, conn, wrongTypeError)
	}
}

func TestGeoSearchInALargeSet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A grid of 2,500 locations, a tenth of a degree apart, around the point
	// where the equator meets the prime meridian.
	for x := -25; x < 25; x++ {
		args := []string{"GEOADD", "grid"}
		for y := -25; y < 25; y++ {
			args = append(args, strconv.FormatFloat(float64(x)/10, 'f', 1, 64), strconv.FormatFloat(float64(y)/10, 'f', 1, 64), strconv.Itoa(x)+","+strconv.Itoa(y))
		}
		do(t, conn, args...)
		expectReply(t, conn, ":50\r\n")
	}

	// A tenth of a degree is 11.1 kilometers there: the center and its four
	// neighbours are within 12 kilometers, the four diagonals within 16.
	expectGeoSearch(t, conn, "grid", []string{"FROMLONLAT", "0", "0", "BYRADIUS", "12", "km"}, "0,0", "-1,0", "1,0", "0,-1", "0,1")
	expectGeoSearch(t, conn, "grid", []string{"FROMLONLAT", "0", "0", "BYRADIUS", "16", "km"},
		"0,0", "-1,0", "1,0", "0,-1", "0,1", "-1,-1", "-1,1", "1,-1", "1,1")
	expectGeoSearchInOrder(t, conn, "grid", []string{"FROMLONLAT", "0.01", "0.02", "BYRADIUS", "100", "km", "COUNT", "1"}, "0,0")
	if got := geoSearch(t, conn, "grid", "FROMLONLAT", "0", "0", "BYRADIUS", "1000", "km"); len(got) != 2500 {
		t.Errorf("a search that covers the whole grid found %d locations, want 2500", len(got))
	}
}

func TestGeoSearchInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{
		{"GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"},
		{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"},
		{"GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"},
		{"GEOSEARCH", "places", "FROMLONLAT", "2", "48", "BYRADIUS", "100", "leagues"},
	} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	do(t, conn, "EXEC")
	expectReply(t, conn, array("*0\r\n", ":1\r\n", bulkArray("Paris"), "-ERR unsupported unit provided. please use M, KM, FT, MI\r\n"))
}

func TestGeoSearchIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)
	conn, watcher := dial(t, addr), dial(t, addr)

	command := []string{"GEOADD", "places", "2.2944692", "48.8584625", "Paris"}
	do(t, conn, command...)
	expectReply(t, conn, ":1\r\n")
	expectPropagated(t, replica, command...)
	do(t, watcher, "WATCH", "places", "missing_key")
	expectReply(t, watcher, "+OK\r\n")

	expectGeoSearch(t, conn, "places", []string{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"}, "Paris")
	expectGeoSearch(t, conn, "missing_key", []string{"FROMLONLAT", "2", "48", "BYRADIUS", "100", "km"})

	// Searching is neither logged nor propagated, and doesn't count as a
	// modification of the key.
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
