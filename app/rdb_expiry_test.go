package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startServerWithRDBAndClock is like startServerWithRDB, but the server's
// notion of time is driven by the returned clock, which is already in place
// when the file is loaded.
func startServerWithRDBAndClock(t *testing.T, clock *fakeClock, file []byte) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dump.rdb"), file, 0o644); err != nil {
		t.Fatalf("writing the RDB file: %v", err)
	}
	cfg, err := parseConfig([]string{"--dir", dir, "--dbfilename", "dump.rdb"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	srv := NewServerWithConfig(cfg)
	srv.store.now = clock.Now
	if err := srv.loadRDBFile(); err != nil {
		t.Fatalf("loading the RDB file: %v", err)
	}
	return startServer(t, srv)
}

func TestKeysOfTheRDBFileWithAnExpiry(t *testing.T) {
	now := time.Now()
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "expired", value: "1", expiresAt: now.Add(-time.Hour)},
		rdbTestKey{key: "valid", value: "2", expiresAt: now.Add(time.Hour)},
		rdbTestKey{key: "forever", value: "3"},
		rdbTestKey{key: "expired_long_ago", value: "4", expiresAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)},
	)))

	do(t, conn, "GET", "expired")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "valid")
	expectReply(t, conn, "$1\r\n2\r\n")
	do(t, conn, "GET", "forever")
	expectReply(t, conn, "$1\r\n3\r\n")
	do(t, conn, "GET", "expired_long_ago")
	expectReply(t, conn, "$-1\r\n")

	// Expired keys don't show up anywhere.
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("forever", "valid"))
	do(t, conn, "TYPE", "expired")
	expectReply(t, conn, "+none\r\n")
}

func TestKeysOfTheRDBFileWithAnExpiryInSeconds(t *testing.T) {
	now := time.Now()
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "expired", value: "1", expiresAt: now.Add(-time.Hour), expiryInSeconds: true},
		rdbTestKey{key: "valid", value: "2", expiresAt: now.Add(time.Hour), expiryInSeconds: true},
	)))

	do(t, conn, "GET", "expired")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "valid")
	expectReply(t, conn, "$1\r\n2\r\n")
}

func TestKeysOfTheRDBFileExpireWhileTheServerRuns(t *testing.T) {
	clock := newFakeClock()
	addr := startServerWithRDBAndClock(t, clock, buildRDB(
		rdbTestKey{key: "soon", value: "1", expiresAt: clock.Now().Add(100 * time.Millisecond)},
		rdbTestKey{key: "later", value: "2", expiresAt: clock.Now().Add(10 * time.Second), expiryInSeconds: true},
		rdbTestKey{key: "forever", value: "3"},
	))
	conn := dial(t, addr)

	do(t, conn, "GET", "soon")
	expectReply(t, conn, "$1\r\n1\r\n")

	clock.Advance(99 * time.Millisecond)
	do(t, conn, "GET", "soon")
	expectReply(t, conn, "$1\r\n1\r\n")

	// The key is gone at the very millisecond of its expiry.
	clock.Advance(time.Millisecond)
	do(t, conn, "GET", "soon")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "later")
	expectReply(t, conn, "$1\r\n2\r\n")

	clock.Advance(time.Minute)
	do(t, conn, "GET", "later")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "forever")
	expectReply(t, conn, "$1\r\n3\r\n")
}

func TestExpiryOnlyAppliesToTheKeyThatFollowsIt(t *testing.T) {
	conn := dial(t, startServerWithRDB(t, buildRDB(
		rdbTestKey{key: "expired", value: "1", expiresAt: time.Now().Add(-time.Hour)},
		// Stored right after an expired key, but with no expiry of its own.
		rdbTestKey{key: "next", value: "2"},
	)))

	do(t, conn, "GET", "expired")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "GET", "next")
	expectReply(t, conn, "$1\r\n2\r\n")
}

func TestSetReplacesTheExpiryOfAKeyOfTheRDBFile(t *testing.T) {
	clock := newFakeClock()
	addr := startServerWithRDBAndClock(t, clock, buildRDB(
		rdbTestKey{key: "foo", value: "1", expiresAt: clock.Now().Add(100 * time.Millisecond)},
		rdbTestKey{key: "counter", value: "41", expiresAt: clock.Now().Add(100 * time.Millisecond)},
	))
	conn := dial(t, addr)

	// SET drops the expiry, INCR keeps it.
	do(t, conn, "SET", "foo", "2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, ":42\r\n")

	clock.Advance(time.Second)
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$1\r\n2\r\n")
	do(t, conn, "GET", "counter")
	expectReply(t, conn, "$-1\r\n")
}

func TestExpiredKeysOfTheRDBFileAreNotLoaded(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()
	file := buildRDB(
		rdbTestKey{key: "expired", value: "1", expiresAt: clock.Now().Add(-time.Second)},
		rdbTestKey{key: "expires_now", value: "2", expiresAt: clock.Now()},
		rdbTestKey{key: "valid", value: "3", expiresAt: clock.Now().Add(time.Second)},
	)
	if err := os.WriteFile(filepath.Join(dir, "dump.rdb"), file, 0o644); err != nil {
		t.Fatalf("writing the RDB file: %v", err)
	}
	cfg, _ := parseConfig([]string{"--dir", dir, "--dbfilename", "dump.rdb"})
	srv := NewServerWithConfig(cfg)
	srv.store.now = clock.Now
	if err := srv.loadRDBFile(); err != nil {
		t.Fatalf("loading the RDB file: %v", err)
	}

	if len(srv.store.entries) != 1 {
		t.Errorf("%d keys were loaded, want only the one that hasn't expired", len(srv.store.entries))
	}
	if _, ok := srv.store.entries["valid"]; !ok {
		t.Errorf("the key that hasn't expired wasn't loaded")
	}
}

func TestParseRDBExpiry(t *testing.T) {
	inMs := time.UnixMilli(1713824559637)
	inSeconds := time.Unix(1714089298, 0)

	keys, err := parseRDB(buildRDB(
		rdbTestKey{key: "ms", value: "1", expiresAt: inMs},
		rdbTestKey{key: "s", value: "2", expiresAt: inSeconds, expiryInSeconds: true},
		rdbTestKey{key: "none", value: "3"},
	))
	if err != nil {
		t.Fatalf("parseRDB: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("keys = %+v, want 3 of them", keys)
	}
	if !keys[0].expiresAt.Equal(inMs) {
		t.Errorf("expiry in milliseconds = %v, want %v", keys[0].expiresAt, inMs)
	}
	if !keys[1].expiresAt.Equal(inSeconds) {
		t.Errorf("expiry in seconds = %v, want %v", keys[1].expiresAt, inSeconds)
	}
	if !keys[2].expiresAt.IsZero() {
		t.Errorf("expiry of the key without one = %v, want none", keys[2].expiresAt)
	}
}
