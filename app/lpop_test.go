package main

import (
	"bufio"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
)

func TestLPop(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "one", "two", "three", "four", "five")
	expectReply(t, conn, ":5\r\n")

	// The first element is removed and returned as a bulk string.
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$3\r\none\r\n")

	// The rest of the list is intact.
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("two", "three", "four", "five"))
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":4\r\n")
}

func TestLPopMissingList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPOP", "missing")
	expectReply(t, conn, "$-1\r\n")

	// Popping from a missing list doesn't create it.
	do(t, conn, "LLEN", "missing")
	expectReply(t, conn, ":0\r\n")
}

func TestLPopReturnsElementsInOrder(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c", "d")
	expectReply(t, conn, ":4\r\n")

	for _, want := range []string{"a", "b", "c", "d"} {
		do(t, conn, "LPOP", "list_key")
		expectReply(t, conn, bulk(want))
	}
}

func TestLPopLastElementDeletesTheList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "only")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$4\r\nonly\r\n")

	// An emptied list is gone: popping again finds nothing...
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, emptyArray)

	// ...and the key isn't a list anymore: reading it as a string is not a
	// type error, it's simply missing.
	do(t, conn, "GET", "list_key")
	expectReply(t, conn, "$-1\r\n")

	// The key can be reused for a new list.
	do(t, conn, "RPUSH", "list_key", "again")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("again"))
}

func TestLPopTakesTheHeadAfterLPush(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "middle", "last")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "LPUSH", "list_key", "first")
	expectReply(t, conn, ":3\r\n")

	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$5\r\nfirst\r\n")
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$6\r\nmiddle\r\n")
}

func TestLPopThenPushAgain(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$1\r\na\r\n")

	do(t, conn, "RPUSH", "list_key", "c")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "LPUSH", "list_key", "z")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("z", "b", "c"))
}

func TestLPopReturnsElementVerbatim(t *testing.T) {
	conn := dial(t, startTestServer(t))

	elements := []string{"", "with space", "a\r\nb", "\x00\xff"}
	do(t, conn, append([]string{"RPUSH", "list_key"}, elements...)...)
	expectReply(t, conn, ":4\r\n")

	// An empty element is an empty bulk string, not a null one.
	for _, want := range elements {
		do(t, conn, "LPOP", "list_key")
		expectReply(t, conn, bulk(want))
	}
}

func TestLPopOnlyAffectsItsList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "first", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "RPUSH", "second", "x", "y")
	expectReply(t, conn, ":2\r\n")

	do(t, conn, "LPOP", "first")
	expectReply(t, conn, "$1\r\na\r\n")

	do(t, conn, "LRANGE", "second", "0", "-1")
	expectReply(t, conn, bulkArray("x", "y"))
}

func TestLPopWithCount(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "one", "two", "three", "four", "five")
	expectReply(t, conn, ":5\r\n")

	// With a count the removed elements come back as an array.
	do(t, conn, "LPOP", "list_key", "2")
	expectReply(t, conn, bulkArray("one", "two"))

	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("three", "four", "five"))
}

func TestLPopWithCountSeveralTimes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c", "d", "e", "f")
	expectReply(t, conn, ":6\r\n")

	do(t, conn, "LPOP", "list_key", "2")
	expectReply(t, conn, bulkArray("a", "b"))
	do(t, conn, "LPOP", "list_key", "3")
	expectReply(t, conn, bulkArray("c", "d", "e"))
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LPOP", "list_key", "1")
	expectReply(t, conn, bulkArray("f"))
}

func TestLPopWithCountOfOneRepliesWithAnArray(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")

	// "LPOP key 1" is an array of one element; "LPOP key" is a bulk string.
	do(t, conn, "LPOP", "list_key", "1")
	expectReply(t, conn, "*1\r\n$1\r\na\r\n")
	do(t, conn, "LPOP", "list_key")
	expectReply(t, conn, "$1\r\nb\r\n")
}

func TestLPopWithCountLargerThanTheList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	// Every element is removed and returned...
	do(t, conn, "LPOP", "list_key", "10")
	expectReply(t, conn, bulkArray("a", "b", "c"))

	// ...which deletes the list.
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "GET", "list_key")
	expectReply(t, conn, "$-1\r\n")
	do(t, conn, "LPOP", "list_key", "10")
	expectReply(t, conn, "*-1\r\n")
}

func TestLPopWithCountEqualToTheLength(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "LPOP", "list_key", "3")
	expectReply(t, conn, bulkArray("a", "b", "c"))
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, emptyArray)
}

func TestLPopWithCountOfZero(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")

	// Nothing is removed: the reply is an empty array.
	do(t, conn, "LPOP", "list_key", "0")
	expectReply(t, conn, emptyArray)
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("a", "b"))
}

func TestLPopWithCountOnMissingList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// With a count, a missing list is a null array rather than a null string.
	do(t, conn, "LPOP", "missing", "2")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "LPOP", "missing", "0")
	expectReply(t, conn, "*-1\r\n")
	do(t, conn, "LPOP", "missing")
	expectReply(t, conn, "$-1\r\n")
}

func TestLPopWithInvalidCount(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	const outOfRange = "-ERR value is out of range, must be positive\r\n"
	for _, count := range []string{"-1", "-100", "two", "1.5", "", "99999999999999999999", "+1", "01", " 1"} {
		do(t, conn, "LPOP", "list_key", count)
		expectReply(t, conn, outOfRange)
	}
	// The count is validated before the key is even looked up.
	do(t, conn, "LPOP", "missing", "-1")
	expectReply(t, conn, outOfRange)

	// Rejected commands removed nothing.
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("a", "b", "c"))
}

func TestLPopWithCountAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "LPOP", "text", "2")
	expectReply(t, conn, wrongTypeError)
}

func TestLPopWithCountReturnsElementsVerbatim(t *testing.T) {
	conn := dial(t, startTestServer(t))

	elements := []string{"", "with space", "a\r\nb", "\x00\xff"}
	do(t, conn, append([]string{"RPUSH", "list_key"}, elements...)...)
	expectReply(t, conn, ":4\r\n")
	do(t, conn, "LPOP", "list_key", "4")
	expectReply(t, conn, bulkArray(elements...))
}

func TestLPopWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LPOP")
	expectReply(t, conn, "-ERR wrong number of arguments for 'lpop' command\r\n")
	do(t, conn, "LPOP", "list_key", "1", "extra")
	expectReply(t, conn, "-ERR wrong number of arguments for 'lpop' command\r\n")
}

func TestLPopAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "LPOP", "text")
	expectReply(t, conn, wrongTypeError)
	do(t, conn, "GET", "text")
	expectReply(t, conn, "$5\r\nvalue\r\n")
}

func TestLPopIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	for i, name := range []string{"lpop", "LPop", "lPOP"} {
		do(t, conn, name, "list_key")
		expectReply(t, conn, bulk(string(rune('a'+i))))
	}
}

func TestConcurrentLPopDeliversEachElementOnce(t *testing.T) {
	addr := startTestServer(t)

	const clients, elements = 8, 200

	setup := dial(t, addr)
	want := make([]string, elements)
	for i := range want {
		want[i] = "element-" + strconv.Itoa(i)
	}
	do(t, setup, append([]string{"RPUSH", "queue"}, want...)...)
	expectReply(t, setup, ":"+strconv.Itoa(elements)+"\r\n")

	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		popped []string
		errs   = make(chan error, clients)
	)
	for i, conn := range conns {
		wg.Add(1)
		go func(id int, conn net.Conn) {
			defer wg.Done()
			reader := bufio.NewReader(conn)
			for {
				if _, err := conn.Write([]byte(encodeCommand("LPOP", "queue"))); err != nil {
					errs <- fmt.Errorf("client %d: write: %w", id, err)
					return
				}
				header, err := reader.ReadString('\n')
				if err != nil {
					errs <- fmt.Errorf("client %d: read: %w", id, err)
					return
				}
				if header == "$-1\r\n" {
					return // the queue is drained
				}
				element, err := reader.ReadString('\n')
				if err != nil {
					errs <- fmt.Errorf("client %d: read: %w", id, err)
					return
				}
				mu.Lock()
				popped = append(popped, element[:len(element)-2])
				mu.Unlock()
			}
		}(i, conn)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Every element was handed to exactly one client.
	sort.Strings(popped)
	sort.Strings(want)
	if !reflect.DeepEqual(popped, want) {
		t.Fatalf("popped %d elements, want each of the %d pushed ones exactly once", len(popped), elements)
	}
}

func TestListPopFront(t *testing.T) {
	l := &list{items: []string{"a", "b", "c", "d", "e"}}

	if got, want := l.popFront(1), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("popFront(1) = %q, want %q", got, want)
	}
	if got, want := l.popFront(2), []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("popFront(2) = %q, want %q", got, want)
	}
	if got := l.popFront(0); len(got) != 0 {
		t.Fatalf("popFront(0) = %q, want nothing", got)
	}
	if got := l.popFront(-3); len(got) != 0 {
		t.Fatalf("popFront(-3) = %q, want nothing", got)
	}
	if want := []string{"d", "e"}; !reflect.DeepEqual(l.items, want) {
		t.Fatalf("items = %q, want %q", l.items, want)
	}

	// Asking for more than what's left returns what's left.
	if got, want := l.popFront(10), []string{"d", "e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("popFront(10) = %q, want %q", got, want)
	}
	if len(l.items) != 0 {
		t.Fatalf("items = %q, want an empty list", l.items)
	}
	if got := l.popFront(1); len(got) != 0 {
		t.Fatalf("popFront(1) on an empty list = %q, want nothing", got)
	}
}

func TestListPopFrontResultIsIndependentFromTheList(t *testing.T) {
	l := &list{items: []string{"a", "b", "c"}}

	popped := l.popFront(2)
	l.pushFront("x", "y")
	l.pushBack("z")

	if want := []string{"a", "b"}; !reflect.DeepEqual(popped, want) {
		t.Fatalf("popped elements changed after later pushes: %q, want %q", popped, want)
	}
	if want := []string{"y", "x", "c", "z"}; !reflect.DeepEqual(l.items, want) {
		t.Fatalf("items = %q, want %q", l.items, want)
	}
}
