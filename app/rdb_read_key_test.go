package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rdbTestKey describes a key of an RDB file built for a test.
type rdbTestKey struct {
	key, value string
	// expiresAt is the key's expiry, if it has one. It is stored in
	// milliseconds, or in seconds if expiryInSeconds is set.
	expiresAt       time.Time
	expiryInSeconds bool
}

// rdbString encodes s as an RDB string: its length, then its bytes.
func rdbString(s string) []byte {
	var b []byte
	switch n := len(s); {
	case n < 1<<6:
		b = append(b, byte(n))
	case n < 1<<14:
		b = append(b, 0x40|byte(n>>8), byte(n))
	default:
		b = append(b, 0x80)
		b = binary.BigEndian.AppendUint32(b, uint32(n))
	}
	return append(b, s...)
}

// buildRDB returns the contents of an RDB file that holds the given keys in
// its database 0, laid out the way Redis writes it.
func buildRDB(keys ...rdbTestKey) []byte {
	withExpiry := 0
	for _, k := range keys {
		if !k.expiresAt.IsZero() {
			withExpiry++
		}
	}

	file := []byte("REDIS0011")
	// Metadata.
	file = append(file, 0xFA)
	file = append(file, rdbString("redis-ver")...)
	file = append(file, rdbString("7.2.0")...)
	file = append(file, 0xFA)
	file = append(file, rdbString("redis-bits")...)
	file = append(file, 0xC0, 0x40) // the integer 64
	// Database 0 and the sizes of its hash tables.
	file = append(file, 0xFE, 0x00, 0xFB, byte(len(keys)), byte(withExpiry))
	for _, k := range keys {
		switch {
		case k.expiresAt.IsZero():
		case k.expiryInSeconds:
			file = append(file, 0xFD)
			file = binary.LittleEndian.AppendUint32(file, uint32(k.expiresAt.Unix()))
		default:
			file = append(file, 0xFC)
			file = binary.LittleEndian.AppendUint64(file, uint64(k.expiresAt.UnixMilli()))
		}
		file = append(file, 0x00) // the value is a string
		file = append(file, rdbString(k.key)...)
		file = append(file, rdbString(k.value)...)
	}
	// End of file and checksum.
	file = append(file, 0xFF)
	return append(file, 0x89, 0x3B, 0xB7, 0x4E, 0xF8, 0x0F, 0x77, 0x19)
}

// startServerWithRDB writes an RDB file with the given contents in a
// temporary directory and runs a server started with --dir and --dbfilename
// pointing at it, the way the program does. It returns the address to dial.
func startServerWithRDB(t *testing.T, file []byte) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dump.rdb"), file, 0o644); err != nil {
		t.Fatalf("writing the RDB file: %v", err)
	}
	return startServerLoadingRDB(t, "--dir", dir, "--dbfilename", "dump.rdb")
}

// startServerLoadingRDB runs a server configured by the command-line
// arguments, after it has loaded its RDB file like the program does at
// startup.
func startServerLoadingRDB(t *testing.T, args ...string) string {
	t.Helper()

	cfg, err := parseConfig(args)
	if err != nil {
		t.Fatalf("parseConfig(%q): %v", args, err)
	}
	srv := NewServerWithConfig(cfg)
	if err := srv.loadRDBFile(); err != nil {
		t.Fatalf("loading the RDB file: %v", err)
	}
	return startServer(t, srv)
}

func TestKeysReturnsTheKeyOfTheRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "foo", value: "bar"})))

	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*1\r\n$3\r\nfoo\r\n")
}

func TestKeysWithoutRDBFile(t *testing.T) {
	// The directory exists, but not the file.
	conn := dial(t, startServerLoadingRDB(t, "--dir", t.TempDir(), "--dbfilename", "missing.rdb"))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	// Neither the directory nor the file exist.
	conn = dial(t, startServerLoadingRDB(t, "--dir", filepath.Join(t.TempDir(), "nope"), "--dbfilename", "dump.rdb"))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestKeysWithAnEmptyRDBFile(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, emptyRDB()))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")

	conn = dial(t, startServerWithRDB(t, buildRDB()))
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestKeysOnAServerWithoutRDBConfiguration(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "KEYS", "*")
	expectReply(t, conn, "*0\r\n")
}

func TestKeysReturnsKeysSetByClients(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(rdbTestKey{key: "from_file", value: "1"})))

	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "XADD", "stream_key", "1-1", "k", "v")
	expectReply(t, conn, "$3\r\n1-1\r\n")

	// Keys of every type, in alphabetical order.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("foo", "from_file", "list_key", "stream_key"))

	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$1\r\na\r\n")
	do(t, conn, "keys", "*")
	expectReply(t, conn, bulkArray("foo", "from_file", "stream_key"))
}

func TestKeysDoesNotReturnExpiredKeys(t *testing.T) {
	addr, clock := startTestServerWithClock(t)
	conn := dial(t, addr)

	do(t, conn, "SET", "short", "1", "PX", "100")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "long", "1", "PX", "10000")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "forever", "1")
	expectReply(t, conn, "+OK\r\n")

	clock.Advance(200 * time.Millisecond)
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("forever", "long"))
}

func TestKeysPatterns(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, key := range []string{"foo", "foobar", "bar", "baz", "user:1", "user:2", "a/b", "h?llo", "hello", "hallo"} {
		do(t, conn, "SET", key, "1")
		expectReply(t, conn, "+OK\r\n")
	}

	tests := []struct {
		pattern string
		want    []string
	}{
		{"f*", []string{"foo", "foobar"}},
		{"*bar", []string{"bar", "foobar"}},
		{"ba?", []string{"bar", "baz"}},
		{"ba[rz]", []string{"bar", "baz"}},
		{"ba[^r]", []string{"baz"}},
		{"user:*", []string{"user:1", "user:2"}},
		{"user:[0-1]", []string{"user:1"}},
		{"a*b", []string{"a/b"}},
		{"h[ae]llo", []string{"hallo", "hello"}},
		{`h\?llo`, []string{"h?llo"}},
		{"h?llo", []string{"h?llo", "hallo", "hello"}},
		{"foo", []string{"foo"}},
		{"nomatch", nil},
		{"", nil},
	}
	for _, tt := range tests {
		do(t, conn, "KEYS", tt.pattern)
		expectReply(t, conn, bulkArray(tt.want...))
	}
}

func TestKeysWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "KEYS")
	expectReply(t, conn, "-ERR wrong number of arguments for 'keys' command\r\n")
	do(t, conn, "KEYS", "*", "extra")
	expectReply(t, conn, "-ERR wrong number of arguments for 'keys' command\r\n")
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"*", "", true},
		{"*", "anything", true},
		{"**", "anything", true},
		{"", "", true},
		{"", "a", false},
		{"a", "", false},
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"a*", "a", true},
		{"a*c", "abbbc", true},
		{"a*c", "abbbd", false},
		{"*a*b*", "xxaxxbxx", true},
		{"?", "a", true},
		{"?", "", false},
		{"?", "ab", false},
		{"[abc]", "b", true},
		{"[abc]", "d", false},
		{"[a-c]x", "bx", true},
		{"[c-a]", "b", true},
		{"[^a-c]", "d", true},
		{"[^a-c]", "b", false},
		{`[\]]`, "]", true},
		{"[a-]", "-", true},
		{"[abc", "b", true},
		{`\*`, "*", true},
		{`\*`, "a", false},
		{`a\`, `a\`, true},
		{"*/*", "a/b", true},
		{"*", "a/b/c", true},
	}
	for _, tt := range tests {
		if got := globMatch(tt.pattern, tt.s); got != tt.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func TestParseRDBSingleKey(t *testing.T) {
	keys, err := parseRDB(buildRDB(rdbTestKey{key: "foo", value: "bar"}))
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 1 || keys[0].key != "foo" {
		t.Fatalf("keys = %+v, want only foo", keys)
	}
}

func TestParseRDBEmptyFile(t *testing.T) {
	for name, file := range map[string][]byte{"written by Redis": emptyRDB(), "built": buildRDB()} {
		keys, err := parseRDB(file)
		if err != nil {
			t.Errorf("%s: parseRDB: %v", name, err)
		}
		if len(keys) != 0 {
			t.Errorf("%s: keys = %+v, want none", name, keys)
		}
	}
}

// TestParseRDBDocumentedExample parses a file assembled from the byte-by-byte
// examples of the format's documentation.
func TestParseRDBDocumentedExample(t *testing.T) {
	file := []byte("REDIS0011")
	file = append(file, 0xFA, 0x09, 'r', 'e', 'd', 'i', 's', '-', 'v', 'e', 'r', 0x06, '6', '.', '0', '.', '1', '6')
	file = append(file, 0xFE, 0x00, 0xFB, 0x03, 0x02)
	file = append(file, 0x00, 0x06, 'f', 'o', 'o', 'b', 'a', 'r', 0x06, 'b', 'a', 'z', 'q', 'u', 'x')
	file = append(file, 0xFC, 0x15, 0x72, 0xE7, 0x07, 0x8F, 0x01, 0x00, 0x00, 0x00, 0x03, 'f', 'o', 'o', 0x03, 'b', 'a', 'r')
	file = append(file, 0xFD, 0x52, 0xED, 0x2A, 0x66, 0x00, 0x03, 'b', 'a', 'z', 0x03, 'q', 'u', 'x')
	file = append(file, 0xFF, 0x89, 0x3B, 0xB7, 0x4E, 0xF8, 0x0F, 0x77, 0x19)

	keys, err := parseRDB(file)
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	want := []rdbKey{
		{key: "foobar", value: "bazqux"},
		{key: "foo", value: "bar", expiresAt: time.UnixMilli(1713824559637)},
		{key: "baz", value: "qux", expiresAt: time.Unix(1714089298, 0)},
	}
	if len(keys) != len(want) {
		t.Fatalf("keys = %+v, want %+v", keys, want)
	}
	for i := range want {
		if keys[i].key != want[i].key || keys[i].value != want[i].value || !keys[i].expiresAt.Equal(want[i].expiresAt) {
			t.Errorf("key %d = %+v, want %+v", i, keys[i], want[i])
		}
	}
}

func TestParseRDBErrors(t *testing.T) {
	valid := buildRDB(rdbTestKey{key: "foo", value: "bar"})

	tests := map[string][]byte{
		"empty":               {},
		"too short":           []byte("REDIS"),
		"wrong magic":         append([]byte("RUBIS0011"), valid[9:]...),
		"invalid version":     append([]byte("REDISabcd"), valid[9:]...),
		"no end of file":      valid[:len(valid)-9],
		"truncated key":       valid[:len(valid)-14],
		"unknown value type":  append(append([]byte("REDIS0011"), 0xFE, 0x00, 0x0E, 0x01, 'k', 0x01, 'v'), 0xFF),
		"truncated expiry":    append([]byte("REDIS0011"), 0xFE, 0x00, 0xFC, 0x01, 0x02),
		"invalid length byte": append([]byte("REDIS0011"), 0xFE, 0x82),
	}
	for name, file := range tests {
		if _, err := parseRDB(file); err == nil {
			t.Errorf("%s: parseRDB succeeded, want an error", name)
		}
	}
}

func TestLoadRDBFileReportsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dump.rdb"), []byte("this is not an RDB file"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	cfg, err := parseConfig([]string{"--dir", dir, "--dbfilename", "dump.rdb"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if err := NewServerWithConfig(cfg).loadRDBFile(); err == nil {
		t.Errorf("loadRDBFile succeeded on a file that isn't an RDB file")
	}
}
