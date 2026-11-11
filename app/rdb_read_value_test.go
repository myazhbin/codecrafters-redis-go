package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestGetReturnsTheValueOfTheRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "foo", value: "bar"})))

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
	do(t, conn, "TYPE", "foo")
	expectReply(t, conn, "+string\r\n")
	do(t, conn, "GET", "missing")
	expectReply(t, conn, "$-1\r\n")
}

func TestValuesOfTheRDBFileOfEveryLength(t *testing.T) {
	// The length of a string is stored on 1, 2 or 5 bytes depending on how
	// large it is.
	for _, length := range []int{0, 1, 63, 64, 700, 16383, 16384, 70000} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			value := strings.Repeat("v", length)
			conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "foo", value: value})))

			do(t, conn, "GET", "foo")
			if got := readBulkString(t, conn); got != value {
				t.Errorf("value has %d bytes, want %d", len(got), length)
			}
		})
	}
}

func TestValueOfTheRDBFileCanBeBinary(t *testing.T) {
	value := "line\r\nbreaks, \x00 nul bytes and \xff\xfe invalid UTF-8"
	conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "foo", value: value})))

	do(t, conn, "GET", "foo")
	if got := readBulkString(t, conn); got != value {
		t.Errorf("value = %q, want %q", got, value)
	}
}

func TestValueOfTheRDBFileCanBeUsedLikeAnyOther(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "counter", value: "41"})))

	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":42\r\n")
	do(t, conn, "SET", "counter", "0")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$1\r\n0\r\n")
}

// rdbWithRawEntry returns an RDB file whose only key is stored with the given
// bytes, which follow the value type.
func rdbWithRawEntry(entry ...byte) []byte {
	file := append([]byte("REDIS0011"), 0xFE, 0x00, 0xFB, 0x01, 0x00, 0x00)
	file = append(file, entry...)
	return append(file, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0)
}

func TestParseRDBIntegerEncodedStrings(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
		want  string
	}{
		{"8 bits", []byte{0xC0, 0x7B}, "123"},
		{"8 bits, negative", []byte{0xC0, 0xFF}, "-1"},
		{"16 bits", []byte{0xC1, 0x39, 0x30}, "12345"},
		{"16 bits, negative", []byte{0xC1, 0x00, 0x80}, "-32768"},
		{"32 bits", []byte{0xC2, 0x87, 0xD6, 0x12, 0x00}, "1234567"},
		{"32 bits, negative", []byte{0xC2, 0xFF, 0xFF, 0xFF, 0xFF}, "-1"},
	}
	for _, tt := range tests {
		keys, err := parseRDB(rdbWithRawEntry(append([]byte{0x03, 'f', 'o', 'o'}, tt.value...)...))
		if err != nil {
			t.Errorf("%s: parseRDB: %v", tt.name, err)
			continue
		}
		if len(keys) != 1 || keys[0].key != "foo" || keys[0].value != tt.want {
			t.Errorf("%s: keys = %+v, want foo = %s", tt.name, keys, tt.want)
		}
	}
}

func TestParseRDBLengthEncodings(t *testing.T) {
	long := strings.Repeat("x", 700)

	tests := []struct {
		name   string
		prefix []byte
		value  string
	}{
		{"6 bits", []byte{0x0D}, "Hello, World!"},
		{"14 bits", []byte{0x42, 0xBC}, long},
		{"32 bits", []byte{0x80, 0x00, 0x00, 0x02, 0xBC}, long},
		{"64 bits", []byte{0x81, 0, 0, 0, 0, 0, 0, 0x02, 0xBC}, long},
	}
	for _, tt := range tests {
		entry := append([]byte{0x03, 'f', 'o', 'o'}, tt.prefix...)
		entry = append(entry, tt.value...)
		keys, err := parseRDB(rdbWithRawEntry(entry...))
		if err != nil {
			t.Errorf("%s: parseRDB: %v", tt.name, err)
			continue
		}
		if len(keys) != 1 || keys[0].value != tt.value {
			t.Errorf("%s: value has %d bytes, want %d", tt.name, len(keys[0].value), len(tt.value))
		}
	}
}

func TestParseRDBCompressedString(t *testing.T) {
	// "aaaaaaaaaa" compressed with LZF: the literal "a", then a reference
	// that copies 9 bytes from 1 byte back.
	compressed := []byte{0x00, 'a', 0xE0, 0x00, 0x00}
	entry := []byte{0x03, 'f', 'o', 'o', 0xC3, byte(len(compressed)), 10}
	entry = append(entry, compressed...)

	keys, err := parseRDB(rdbWithRawEntry(entry...))
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 1 || keys[0].value != "aaaaaaaaaa" {
		t.Errorf("keys = %+v, want foo = aaaaaaaaaa", keys)
	}
}

func TestLzfDecompress(t *testing.T) {
	tests := []struct {
		name       string
		compressed []byte
		want       string
	}{
		{"literals only", []byte{0x04, 'h', 'e', 'l', 'l', 'o'}, "hello"},
		{"back reference", []byte{0x02, 'a', 'b', 'c', 0x20, 0x02}, "abcabc"},
		{"overlapping reference", []byte{0x00, 'a', 0xE0, 0x00, 0x00}, "aaaaaaaaaa"},
		{"literals after a reference", []byte{0x01, 'a', 'b', 0x20, 0x01, 0x00, '!'}, "ababa!"},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		got, err := lzfDecompress(tt.compressed, len(tt.want))
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tt.name, err)
			continue
		}
		if string(got) != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestLzfDecompressErrors(t *testing.T) {
	tests := []struct {
		name       string
		compressed []byte
		length     int
	}{
		{"truncated literals", []byte{0x04, 'h', 'e'}, 5},
		{"reference before the start", []byte{0x00, 'a', 0x20, 0x05}, 4},
		{"truncated reference", []byte{0x00, 'a', 0x20}, 4},
		{"truncated long reference", []byte{0x00, 'a', 0xE0}, 10},
		{"shorter than announced", []byte{0x01, 'a', 'b'}, 5},
		{"longer than announced", []byte{0x04, 'h', 'e', 'l', 'l', 'o'}, 2},
	}
	for _, tt := range tests {
		if _, err := lzfDecompress(tt.compressed, tt.length); err == nil {
			t.Errorf("%s: lzfDecompress succeeded, want an error", tt.name)
		}
	}
}
