package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const wrongTypeError = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"

func TestRPushCreatesList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The reply is the length of the list after the push, as a RESP integer.
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
}

func TestRPushCreatesIndependentLists(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Every key gets its own list: each of these pushes creates a new one.
	for i := 0; i < 5; i++ {
		do(t, conn, "RPUSH", randomWord(t), randomWord(t))
		expectReply(t, conn, ":1\r\n")
	}
}

// listItems returns a copy of the elements of the list stored at key, read
// straight from the server's keyspace.
func listItems(t *testing.T, srv *Server, key string) []string {
	t.Helper()

	srv.mu.Lock()
	defer srv.mu.Unlock()

	l, err := srv.store.getList(key)
	if err != nil {
		t.Fatalf("reading list %q: %v", key, err)
	}
	if l == nil {
		return nil
	}
	return append([]string(nil), l.items...)
}

func TestRPushAppendsToExistingList(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Each push on the same key appends, and replies with the new length.
	do(t, conn, "RPUSH", "list_key", "element1")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "RPUSH", "list_key", "element2")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "RPUSH", "list_key", "element3")
	expectReply(t, conn, ":3\r\n")

	// New elements go to the end (the "right") of the list.
	want := []string{"element1", "element2", "element3"}
	if got := listItems(t, srv, "list_key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %q, want %q", got, want)
	}
}

func TestRPushLengthKeepsGrowing(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for i := 1; i <= 25; i++ {
		do(t, conn, "RPUSH", "list_key", randomWord(t))
		expectReply(t, conn, ":"+strconv.Itoa(i)+"\r\n")
	}
}

func TestRPushSameElementTwice(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Lists aren't sets: duplicates are kept.
	do(t, conn, "RPUSH", "list_key", "same")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "RPUSH", "list_key", "same")
	expectReply(t, conn, ":2\r\n")

	want := []string{"same", "same"}
	if got := listItems(t, srv, "list_key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %q, want %q", got, want)
	}
}

func TestRPushOnOneListLeavesOthersAlone(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "first", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "RPUSH", "first", "b")
	expectReply(t, conn, ":2\r\n")

	// A different key starts its own list from scratch.
	do(t, conn, "RPUSH", "second", "x")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "RPUSH", "first", "c")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "RPUSH", "second", "y")
	expectReply(t, conn, ":2\r\n")
}

func TestRPushFromSeveralClientsAppendsToTheSameList(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "RPUSH", "shared", "from-first")
	expectReply(t, first, ":1\r\n")
	do(t, second, "RPUSH", "shared", "from-second")
	expectReply(t, second, ":2\r\n")
	do(t, first, "RPUSH", "shared", "from-first-again")
	expectReply(t, first, ":3\r\n")
}

func TestConcurrentRPushLosesNoElement(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)

	const clients, pushesPerClient = 10, 20

	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		lengths []int // every length replied to any client
		errs    = make(chan error, clients)
	)
	for i, conn := range conns {
		wg.Add(1)
		go func(id int, conn net.Conn) {
			defer wg.Done()
			reader := make([]byte, 16)
			for n := 0; n < pushesPerClient; n++ {
				element := fmt.Sprintf("client-%d-element-%d", id, n)
				if _, err := conn.Write([]byte(encodeCommand("RPUSH", "shared", element))); err != nil {
					errs <- fmt.Errorf("client %d: write: %w", id, err)
					return
				}
				// Replies look like ":<length>\r\n"; read up to the newline.
				var reply strings.Builder
				for !strings.HasSuffix(reply.String(), "\r\n") {
					k, err := conn.Read(reader[:1])
					if err != nil && err != io.EOF {
						errs <- fmt.Errorf("client %d: read: %w", id, err)
						return
					}
					if k == 0 {
						errs <- fmt.Errorf("client %d: connection closed mid reply (%q)", id, reply.String())
						return
					}
					reply.WriteByte(reader[0])
				}
				text := reply.String()
				length, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(text, ":"), "\r\n"))
				if err != nil || !strings.HasPrefix(text, ":") {
					errs <- fmt.Errorf("client %d: reply %q is not an integer", id, text)
					return
				}
				mu.Lock()
				lengths = append(lengths, length)
				mu.Unlock()
			}
		}(i, conn)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Pushes are atomic: the replied lengths are exactly 1..total, each once.
	total := clients * pushesPerClient
	sort.Ints(lengths)
	for i, length := range lengths {
		if length != i+1 {
			t.Fatalf("replied lengths are not 1..%d: position %d holds %d", total, i, length)
		}
	}

	items := listItems(t, srv, "shared")
	if len(items) != total {
		t.Fatalf("list holds %d elements, want %d", len(items), total)
	}
	// Each client's own elements kept their relative order.
	next := make(map[string]int)
	for _, item := range items {
		parts := strings.Split(item, "-element-")
		n, _ := strconv.Atoi(parts[1])
		if n != next[parts[0]] {
			t.Fatalf("%s: element %d found where element %d was expected", parts[0], n, next[parts[0]])
		}
		next[parts[0]]++
	}
}

func TestRPushMultipleElements(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Several elements can create a list in one go...
	do(t, conn, "RPUSH", "list_key", "element1", "element2", "element3")
	expectReply(t, conn, ":3\r\n")

	// ...and be appended to an existing one.
	do(t, conn, "RPUSH", "list_key", "element4", "element5")
	expectReply(t, conn, ":5\r\n")

	// Elements are appended left to right, in the order they were given.
	want := []string{"element1", "element2", "element3", "element4", "element5"}
	if got := listItems(t, srv, "list_key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %q, want %q", got, want)
	}
}

func TestRPushMixesSingleAndMultipleElements(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "RPUSH", "another_list", "bar", "baz")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "RPUSH", "another_list", "foo", "bar", "baz")
	expectReply(t, conn, ":5\r\n")
	do(t, conn, "RPUSH", "another_list", "single")
	expectReply(t, conn, ":6\r\n")

	want := []string{"bar", "baz", "foo", "bar", "baz", "single"}
	if got := listItems(t, srv, "another_list"); !reflect.DeepEqual(got, want) {
		t.Fatalf("list = %q, want %q", got, want)
	}
}

func TestRPushManyElementsAtOnce(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	const count = 500
	elements := make([]string, count)
	for i := range elements {
		elements[i] = "element-" + strconv.Itoa(i)
	}

	do(t, conn, append([]string{"RPUSH", "big"}, elements...)...)
	expectReply(t, conn, ":500\r\n")

	if got := listItems(t, srv, "big"); !reflect.DeepEqual(got, elements) {
		t.Fatalf("list holds %d elements that differ from the %d pushed ones", len(got), count)
	}
}

func TestRPushMultipleEdgeCaseElements(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	// Elements are binary-safe strings; empty and duplicate ones are fine.
	elements := []string{"", "with space", "a\r\nb", "\x00\xff", "dup", "dup", ""}
	do(t, conn, append([]string{"RPUSH", "list_key"}, elements...)...)
	expectReply(t, conn, ":7\r\n")

	if got := listItems(t, srv, "list_key"); !reflect.DeepEqual(got, elements) {
		t.Fatalf("list = %q, want %q", got, elements)
	}
}

func TestRPushMultipleElementsAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "RPUSH", "text", "a", "b", "c")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "GET", "text")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}

func TestRPushCommandNameIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, name := range []string{"rpush", "RPush", "rPUSH"} {
		do(t, conn, name, "list-"+name, "element")
		expectReply(t, conn, ":1\r\n")
	}
}

func TestRPushWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH")
	expectReply(t, conn, "-ERR wrong number of arguments for 'rpush' command\r\n")
	do(t, conn, "RPUSH", "list_key")
	expectReply(t, conn, "-ERR wrong number of arguments for 'rpush' command\r\n")

	// The failed commands did not create the list.
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
}

func TestRPushAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")

	do(t, conn, "RPUSH", "text", "element")
	expectReply(t, conn, wrongTypeError)

	// The string is left untouched.
	do(t, conn, "GET", "text")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}

func TestGetAgainstListIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "GET", "list_key")
	expectReply(t, conn, wrongTypeError)
}

func TestSetReplacesList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "key", "element")
	expectReply(t, conn, ":1\r\n")

	// SET overwrites a key whatever it held before.
	do(t, conn, "SET", "key", "now a string")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "key")
	expectReply(t, conn, "$12\r\nnow a string\r\n")
}

func TestStoreLists(t *testing.T) {
	st := newStore()

	if l, err := st.getList("missing"); l != nil || err != nil {
		t.Fatalf(`getList("missing") = (%v, %v), want (nil, nil)`, l, err)
	}

	created, err := st.getOrCreateList("list")
	if err != nil || created == nil {
		t.Fatalf(`getOrCreateList("list") = (%v, %v), want a new list`, created, err)
	}
	created.items = append(created.items, "a")

	// The same list is returned from then on, with its content.
	again, err := st.getOrCreateList("list")
	if err != nil || again != created {
		t.Fatalf("getOrCreateList returned a different list for an existing key (err: %v)", err)
	}
	found, err := st.getList("list")
	if err != nil || found != created || len(found.items) != 1 || found.items[0] != "a" {
		t.Fatalf(`getList("list") = (%v, %v), want the list holding "a"`, found, err)
	}
}

func TestStoreReportsWrongType(t *testing.T) {
	st := newStore()
	st.setString("text", "value", noExpiry)
	if _, err := st.getOrCreateList("list"); err != nil {
		t.Fatalf("creating a list: %v", err)
	}

	if _, err := st.getList("text"); !errors.Is(err, errWrongType) {
		t.Errorf(`getList on a string: err = %v, want errWrongType`, err)
	}
	if _, err := st.getOrCreateList("text"); !errors.Is(err, errWrongType) {
		t.Errorf(`getOrCreateList on a string: err = %v, want errWrongType`, err)
	}
	if _, _, err := st.getString("list"); !errors.Is(err, errWrongType) {
		t.Errorf(`getString on a list: err = %v, want errWrongType`, err)
	}

	// A failed access must not have replaced the string by a list.
	if value, ok, err := st.getString("text"); !ok || value != "value" || err != nil {
		t.Errorf(`getString("text") = (%q, %v, %v), want ("value", true, nil)`, value, ok, err)
	}
}
