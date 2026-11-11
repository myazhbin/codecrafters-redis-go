package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
)

// randomWord returns a random string, so that tests can't pass with hardcoded
// replies.
func randomWord(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a random word: %v", err)
	}
	return hex.EncodeToString(raw)
}

// bulk returns the RESP bulk string encoding of s.
func bulk(s string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s)
}

func TestSetThenGet(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "bar")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$3\r\nbar\r\n")
}

func TestSetThenGetRandomKeysAndValues(t *testing.T) {
	conn := dial(t, startTestServer(t))

	pairs := make(map[string]string)
	for i := 0; i < 10; i++ {
		key, value := randomWord(t), randomWord(t)
		pairs[key] = value

		do(t, conn, "SET", key, value)
		expectReply(t, conn, "+OK\r\n")
	}

	// Every key keeps its own value.
	for key, value := range pairs {
		do(t, conn, "GET", key)
		expectReply(t, conn, bulk(value))
	}
}

func TestGetMissingKey(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A missing key is a null bulk string, not an empty string.
	do(t, conn, "GET", "missing")
	expectReply(t, conn, "$-1\r\n")
}

func TestSetOverwritesExistingValue(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "foo", "first")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SET", "foo", "second value")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$12\r\nsecond value\r\n")
}

func TestSetGetEdgeCaseValues(t *testing.T) {
	conn := dial(t, startTestServer(t))

	tests := []struct {
		name, key, value string
	}{
		{"empty value", "empty", ""},
		{"empty key", "", "value of the empty key"},
		{"value with spaces", "spaces", "hello  big world"},
		{"value with CRLF", "crlf", "line one\r\nline two"},
		{"binary value", "binary", "\x00\x01\xff\xfe"},
		{"key with spaces and CRLF", "a key\r\nwith stuff", "value"},
		{"multi-byte characters", "ключ", "значение ✓"},
		{"numeric value", "number", "12345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			do(t, conn, "SET", tt.key, tt.value)
			expectReply(t, conn, "+OK\r\n")

			do(t, conn, "GET", tt.key)
			expectReply(t, conn, bulk(tt.value))
		})
	}
}

func TestKeysAreCaseSensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "set", "Key", "upper")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "SeT", "key", "lower")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "get", "Key")
	expectReply(t, conn, "$5\r\nupper\r\n")
	do(t, conn, "GeT", "key")
	expectReply(t, conn, "$5\r\nlower\r\n")
	do(t, conn, "GET", "KEY")
	expectReply(t, conn, "$-1\r\n")
}

func TestKeysAreSharedBetweenClients(t *testing.T) {
	addr := startTestServer(t)
	writer := dial(t, addr)
	reader := dial(t, addr)

	do(t, writer, "SET", "shared", "value")
	expectReply(t, writer, "+OK\r\n")

	do(t, reader, "GET", "shared")
	expectReply(t, reader, "$5\r\nvalue\r\n")
}

func TestServersDoNotShareKeys(t *testing.T) {
	first := dial(t, startTestServer(t))
	second := dial(t, startTestServer(t))

	do(t, first, "SET", "foo", "bar")
	expectReply(t, first, "+OK\r\n")

	do(t, second, "GET", "foo")
	expectReply(t, second, "$-1\r\n")
}

func TestSetGetWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET")
	expectReply(t, conn, "-ERR wrong number of arguments for 'set' command\r\n")
	do(t, conn, "SET", "foo")
	expectReply(t, conn, "-ERR wrong number of arguments for 'set' command\r\n")
	do(t, conn, "GET")
	expectReply(t, conn, "-ERR wrong number of arguments for 'get' command\r\n")
	do(t, conn, "GET", "foo", "bar")
	expectReply(t, conn, "-ERR wrong number of arguments for 'get' command\r\n")

	// None of the failed commands created the key.
	do(t, conn, "GET", "foo")
	expectReply(t, conn, "$-1\r\n")
}

func TestConcurrentSetAndGet(t *testing.T) {
	addr := startTestServer(t)

	const clients, rounds = 16, 25

	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i, conn := range conns {
		wg.Add(1)
		go func(id int, conn net.Conn) {
			defer wg.Done()

			// roundTrip sends one command and compares the raw reply.
			roundTrip := func(want string, args ...string) error {
				if _, err := conn.Write([]byte(encodeCommand(args...))); err != nil {
					return fmt.Errorf("client %d: write: %w", id, err)
				}
				got := make([]byte, len(want))
				if _, err := io.ReadFull(conn, got); err != nil {
					return fmt.Errorf("client %d: read: %w", id, err)
				}
				if string(got) != want {
					return fmt.Errorf("client %d: %q replied %q, want %q", id, args, got, want)
				}
				return nil
			}

			for n := 0; n < rounds; n++ {
				// Each client owns one key, so it must always read back the
				// value it wrote last, whatever the other clients are doing.
				key := fmt.Sprintf("key-%d", id)
				value := fmt.Sprintf("value-%d-%d", id, n)

				if err := roundTrip("+OK\r\n", "SET", key, value); err != nil {
					errs <- err
					return
				}
				if err := roundTrip(bulk(value), "GET", key); err != nil {
					errs <- err
					return
				}
				// Everybody also hammers one shared key to exercise locking.
				if err := roundTrip("+OK\r\n", "SET", "contended", value); err != nil {
					errs <- err
					return
				}
			}
		}(i, conn)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func TestStoreStrings(t *testing.T) {
	st := newStore()

	if value, ok, err := st.getString("missing"); ok || value != "" || err != nil {
		t.Fatalf(`getString("missing") = (%q, %v, %v), want ("", false, nil)`, value, ok, err)
	}

	st.setString("key", "value", noExpiry)
	if value, ok, err := st.getString("key"); !ok || value != "value" || err != nil {
		t.Fatalf(`getString("key") = (%q, %v, %v), want ("value", true, nil)`, value, ok, err)
	}

	// An empty value is still a value: the key exists.
	st.setString("key", "", noExpiry)
	if value, ok, err := st.getString("key"); !ok || value != "" || err != nil {
		t.Fatalf(`getString("key") after overwrite = (%q, %v, %v), want ("", true, nil)`, value, ok, err)
	}
}
