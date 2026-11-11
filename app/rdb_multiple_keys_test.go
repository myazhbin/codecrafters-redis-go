package main

import (
	"sort"
	"strconv"
	"testing"
)

func TestKeysReturnsEveryKeyOfTheRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "foo", value: "1"},
		rdbTestKey{key: "bar", value: "2"},
	)))

	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*2\r\n$3\r\nbar\r\n$3\r\nfoo\r\n")
}

func TestKeysReturnsManyKeysOfTheRDBFile(t *testing.T) {
	var (
		keys []rdbTestKey
		want []string
	)
	for i := range 200 {
		key := "key:" + strconv.Itoa(i)
		keys = append(keys, rdbTestKey{key: key, value: "v"})
		want = append(want, key)
	}
	sort.Strings(want)

	conn := dial(t, startServerWithRDB(t, buildRDB(keys...)))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray(want...))
	do(t, conn, "KEYS", "key:19?")
	expectReply(t, conn, bulkArray("key:190", "key:191", "key:192", "key:193", "key:194",
		"key:195", "key:196", "key:197", "key:198", "key:199"))
}

func TestParseRDBKeepsTheKeysInFileOrder(t *testing.T) {
	keys, err := parseRDB(buildRDB(
		rdbTestKey{key: "pear", value: "1"},
		rdbTestKey{key: "apple", value: "2"},
		rdbTestKey{key: "mango", value: "3"},
	))
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	var got []string
	for _, k := range keys {
		got = append(got, k.key)
	}
	if want := []string{"pear", "apple", "mango"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("keys = %q, want %q", got, want)
	}
}

func TestParseRDBOnlyReadsTheFirstDatabase(t *testing.T) {
	file := []byte("REDIS0011")
	// Database 0.
	file = append(file, 0xFE, 0x00, 0xFB, 0x01, 0x00)
	file = append(file, 0x00, 0x03, 'f', 'o', 'o', 0x03, 'b', 'a', 'r')
	// Database 1, which the server doesn't have.
	file = append(file, 0xFE, 0x01, 0xFB, 0x01, 0x00)
	file = append(file, 0x00, 0x05, 'o', 't', 'h', 'e', 'r', 0x01, 'x')
	file = append(file, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0)

	keys, err := parseRDB(file)
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 1 || keys[0].key != "foo" {
		t.Errorf("keys = %+v, want only foo", keys)
	}
}

func TestParseRDBWithoutOptionalSections(t *testing.T) {
	// No metadata and no hash table sizes: just the keys.
	file := append([]byte("REDIS0011"), 0xFE, 0x00)
	file = append(file, 0x00, 0x01, 'a', 0x01, '1')
	file = append(file, 0x00, 0x01, 'b', 0x01, '2')
	file = append(file, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0)

	keys, err := parseRDB(file)
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 2 || keys[0].key != "a" || keys[1].key != "b" {
		t.Errorf("keys = %+v, want a and b", keys)
	}
}

func TestParseRDBSkipsPerKeyMetadata(t *testing.T) {
	// Keys can be preceded by their idle time or their access frequency.
	file := append([]byte("REDIS0011"), 0xFE, 0x00)
	file = append(file, 0xF8, 0x0A, 0x00, 0x01, 'a', 0x01, '1')
	file = append(file, 0xF9, 0x05, 0x00, 0x01, 'b', 0x01, '2')
	file = append(file, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0)

	keys, err := parseRDB(file)
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 2 || keys[0].key != "a" || keys[1].key != "b" {
		t.Errorf("keys = %+v, want a and b", keys)
	}
}
