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
	"time"
)

// blockedWait is how long tests watch a client to conclude that it is blocked.
const blockedWait = 100 * time.Millisecond

// waitForBlockedClients waits until exactly n clients are blocked on key.
func waitForBlockedClients(t *testing.T, srv *Server, key string, n int) {
	t.Helper()

	eventually(t, fmt.Sprintf("%d client(s) to be blocked on %q", n, key), func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.listWaiters[key]) == n
	})
}

func TestBLPopReturnsAvailableElementImmediately(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "first", "second")
	expectReply(t, conn, ":2\r\n")

	// The reply holds the key the element came from, then the element.
	do(t, conn, "BLPOP", "list_key", "0")
	expectReply(t, conn, bulkArray("list_key", "first"))

	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("second"))
}

func TestBLPopBlocksUntilAnElementIsPushed(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	do(t, blocked, "BLPOP", "list_key", "0")
	expectNoReply(t, blocked, blockedWait)

	// The pusher sees the length of the list right after its push...
	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")

	// ...and the blocked client receives the element right away.
	expectReply(t, blocked, "*2\r\n$8\r\nlist_key\r\n$3\r\nfoo\r\n")

	// The element was consumed: the list is gone and nobody waits anymore.
	do(t, pusher, "LLEN", "list_key")
	expectReply(t, pusher, ":0\r\n")
	do(t, pusher, "GET", "list_key")
	expectReply(t, pusher, "$-1\r\n")
	waitForBlockedClients(t, srv, "list_key", 0)
}

func TestBLPopIsUnblockedByLPush(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	do(t, blocked, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)

	do(t, pusher, "LPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, blocked, bulkArray("list_key", "foo"))
}

func TestBLPopServesTheLongestWaitingClientFirst(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	first := dial(t, addr)
	second := dial(t, addr)
	pusher := dial(t, addr)

	do(t, first, "BLPOP", "another_list_key", "0")
	waitForBlockedClients(t, srv, "another_list_key", 1)
	do(t, second, "BLPOP", "another_list_key", "0")
	waitForBlockedClients(t, srv, "another_list_key", 2)

	// A single element only unblocks the client that started waiting first.
	do(t, pusher, "RPUSH", "another_list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, first, bulkArray("another_list_key", "foo"))
	expectNoReply(t, second, blockedWait)

	// The next element goes to the second client.
	do(t, pusher, "RPUSH", "another_list_key", "bar")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, second, bulkArray("another_list_key", "bar"))
}

func TestBLPopManyClientsAreServedInArrivalOrder(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	pusher := dial(t, addr)

	const clients = 6
	blocked := make([]net.Conn, clients)
	for i := range blocked {
		blocked[i] = dial(t, addr)
		do(t, blocked[i], "BLPOP", "queue", "0")
		waitForBlockedClients(t, srv, "queue", i+1)
	}

	for i := range blocked {
		element := "element-" + strconv.Itoa(i)
		do(t, pusher, "RPUSH", "queue", element)
		expectReply(t, pusher, ":1\r\n")
		expectReply(t, blocked[i], bulkArray("queue", element))
	}
}

func TestBLPopPushOfSeveralElementsServesSeveralClients(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	first := dial(t, addr)
	second := dial(t, addr)
	pusher := dial(t, addr)

	do(t, first, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)
	do(t, second, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 2)

	// Three elements for two waiting clients: one each, and one stays.
	do(t, pusher, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, pusher, ":3\r\n")
	expectReply(t, first, bulkArray("list_key", "a"))
	expectReply(t, second, bulkArray("list_key", "b"))

	do(t, pusher, "LRANGE", "list_key", "0", "-1")
	expectReply(t, pusher, bulkArray("c"))
}

func TestBLPopClientCanBlockAgain(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	for _, element := range []string{"one", "two", "three"} {
		do(t, blocked, "BLPOP", "list_key", "0")
		waitForBlockedClients(t, srv, "list_key", 1)

		do(t, pusher, "RPUSH", "list_key", element)
		expectReply(t, pusher, ":1\r\n")
		expectReply(t, blocked, bulkArray("list_key", element))
	}

	// Once unblocked the client is back to normal.
	do(t, blocked, "PING")
	expectReply(t, blocked, "+PONG\r\n")
}

func TestBLPopDoesNotBlockOtherClients(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	other := dial(t, addr)

	do(t, blocked, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)

	// The rest of the server keeps working while a client is blocked.
	do(t, other, "PING")
	expectReply(t, other, "+PONG\r\n")
	do(t, other, "SET", "foo", "bar")
	expectReply(t, other, "+OK\r\n")
	do(t, other, "GET", "foo")
	expectReply(t, other, "$3\r\nbar\r\n")
	do(t, other, "RPUSH", "unrelated", "x")
	expectReply(t, other, ":1\r\n")

	// Pushing to another list doesn't wake the blocked client up.
	expectNoReply(t, blocked, blockedWait)
}

func TestBLPopWithSeveralKeys(t *testing.T) {
	t.Run("pops from the first list that has an element", func(t *testing.T) {
		conn := dial(t, startTestServer(t))

		do(t, conn, "RPUSH", "second", "from-second")
		expectReply(t, conn, ":1\r\n")
		do(t, conn, "RPUSH", "third", "from-third")
		expectReply(t, conn, ":1\r\n")

		do(t, conn, "BLPOP", "first", "second", "third", "0")
		expectReply(t, conn, bulkArray("second", "from-second"))

		do(t, conn, "LLEN", "third")
		expectReply(t, conn, ":1\r\n")
	})

	t.Run("is unblocked by a push to any of the lists", func(t *testing.T) {
		srv := NewServer()
		addr := startServer(t, srv)
		blocked := dial(t, addr)
		pusher := dial(t, addr)

		do(t, blocked, "BLPOP", "first", "second", "0")
		waitForBlockedClients(t, srv, "first", 1)
		waitForBlockedClients(t, srv, "second", 1)

		do(t, pusher, "RPUSH", "second", "value")
		expectReply(t, pusher, ":1\r\n")
		expectReply(t, blocked, bulkArray("second", "value"))

		// The client stopped waiting on the other list too: an element
		// pushed there now stays in the list.
		waitForBlockedClients(t, srv, "first", 0)
		do(t, pusher, "RPUSH", "first", "kept")
		expectReply(t, pusher, ":1\r\n")
		do(t, pusher, "LRANGE", "first", "0", "-1")
		expectReply(t, pusher, bulkArray("kept"))
	})

	t.Run("the same key given twice waits only once", func(t *testing.T) {
		srv := NewServer()
		addr := startServer(t, srv)
		blocked := dial(t, addr)
		pusher := dial(t, addr)

		do(t, blocked, "BLPOP", "list_key", "list_key", "0")
		waitForBlockedClients(t, srv, "list_key", 1)

		do(t, pusher, "RPUSH", "list_key", "a", "b")
		expectReply(t, pusher, ":2\r\n")
		expectReply(t, blocked, bulkArray("list_key", "a"))
		do(t, pusher, "LRANGE", "list_key", "0", "-1")
		expectReply(t, pusher, bulkArray("b"))
	})
}

func TestBLPopDisconnectedClientDoesNotConsumeElements(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	leaving := dial(t, addr)
	staying := dial(t, addr)
	pusher := dial(t, addr)

	do(t, leaving, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)
	do(t, staying, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 2)

	// The first client in line goes away: the server forgets about it.
	leaving.Close()
	waitForBlockedClients(t, srv, "list_key", 1)

	// The element goes to the client that is still there instead of being lost.
	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, staying, bulkArray("list_key", "foo"))
}

func TestBLPopElementStaysInListWhenTheOnlyWaiterLeft(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	leaving := dial(t, addr)
	pusher := dial(t, addr)

	do(t, leaving, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 1)
	leaving.Close()
	waitForBlockedClients(t, srv, "list_key", 0)

	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	do(t, pusher, "LRANGE", "list_key", "0", "-1")
	expectReply(t, pusher, bulkArray("foo"))
}

func TestBLPopTimesOut(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)

	start := time.Now()
	do(t, conn, "BLPOP", "list_key", "0.1")

	// Nothing comes back while the timeout is running...
	expectNoReply(t, conn, 50*time.Millisecond)

	// ...then the reply is a null array.
	expectReply(t, conn, "*-1\r\n")
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("BLPOP timed out after %v, before its 100ms timeout", elapsed)
	}

	// The client isn't waiting anymore: a later push stays in the list.
	waitForBlockedClients(t, srv, "list_key", 0)
	do(t, conn, "RPUSH", "list_key", "late")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LRANGE", "list_key", "0", "-1")
	expectReply(t, conn, bulkArray("late"))
}

func TestBLPopWithTimeoutReturnsElementPushedInTime(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	do(t, blocked, "BLPOP", "list_key", "0.3")
	waitForBlockedClients(t, srv, "list_key", 1)

	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, blocked, bulkArray("list_key", "foo"))

	// The timeout was cancelled: no null array follows once it has elapsed.
	expectNoReply(t, blocked, 400*time.Millisecond)
	do(t, blocked, "PING")
	expectReply(t, blocked, "+PONG\r\n")
}

func TestBLPopWithTimeoutReturnsAvailableElementImmediately(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "ready")
	expectReply(t, conn, ":1\r\n")

	start := time.Now()
	do(t, conn, "BLPOP", "list_key", "5")
	expectReply(t, conn, bulkArray("list_key", "ready"))
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("BLPOP took %v although an element was available", elapsed)
	}
}

func TestBLPopTimeoutIsInSeconds(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	// "1" is one second, not one millisecond: after 150ms the client must
	// still be waiting.
	do(t, blocked, "BLPOP", "list_key", "1")
	expectNoReply(t, blocked, 150*time.Millisecond)
	waitForBlockedClients(t, srv, "list_key", 1)

	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, blocked, bulkArray("list_key", "foo"))
}

func TestBLPopTimeoutFormats(t *testing.T) {
	// All of these are valid ways to write a short timeout.
	for _, timeout := range []string{"0.05", ".05", "5e-2", "0.050", "+0.05"} {
		t.Run(timeout, func(t *testing.T) {
			conn := dial(t, startTestServer(t))

			do(t, conn, "BLPOP", "list_key", timeout)
			expectReply(t, conn, "*-1\r\n")
		})
	}
}

func TestBLPopClientIsUsableAfterTimeout(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)
	pusher := dial(t, addr)

	do(t, conn, "BLPOP", "list_key", "0.05")
	expectReply(t, conn, "*-1\r\n")

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")

	// It can block again, and be served this time.
	do(t, conn, "BLPOP", "list_key", "2")
	waitForBlockedClients(t, srv, "list_key", 1)
	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, conn, bulkArray("list_key", "foo"))
}

func TestBLPopTimedOutClientLosesItsTurn(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	impatient := dial(t, addr)
	patient := dial(t, addr)
	pusher := dial(t, addr)

	// The impatient client is first in line, but gives up after 50ms.
	do(t, impatient, "BLPOP", "list_key", "0.05")
	waitForBlockedClients(t, srv, "list_key", 1)
	do(t, patient, "BLPOP", "list_key", "0")
	waitForBlockedClients(t, srv, "list_key", 2)

	expectReply(t, impatient, "*-1\r\n")
	waitForBlockedClients(t, srv, "list_key", 1)

	// The element goes to the client that is still waiting.
	do(t, pusher, "RPUSH", "list_key", "foo")
	expectReply(t, pusher, ":1\r\n")
	expectReply(t, patient, bulkArray("list_key", "foo"))
	expectNoReply(t, impatient, blockedWait)
}

func TestBLPopTimeoutWithSeveralKeys(t *testing.T) {
	t.Parallel()

	srv := NewServer()
	addr := startServer(t, srv)
	conn := dial(t, addr)

	do(t, conn, "BLPOP", "first", "second", "0.05")
	expectReply(t, conn, "*-1\r\n")

	// The client was removed from the queue of every key.
	waitForBlockedClients(t, srv, "first", 0)
	waitForBlockedClients(t, srv, "second", 0)
}

func TestBLPopEachClientHasItsOwnTimeout(t *testing.T) {
	t.Parallel()

	addr := startTestServer(t)
	short := dial(t, addr)
	long := dial(t, addr)

	do(t, long, "BLPOP", "list_key", "0.4")
	do(t, short, "BLPOP", "list_key", "0.05")

	// The short timeout fires while the long one is still running.
	expectReply(t, short, "*-1\r\n")
	expectNoReply(t, long, blockedWait)
	expectReply(t, long, "*-1\r\n")
}

func TestBLPopTimeoutOutOfRange(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "BLPOP", "list_key", "1e300")
	expectReply(t, conn, "-ERR timeout is out of range\r\n")
	do(t, conn, "BLPOP", "list_key", "99999999999999999999")
	expectReply(t, conn, "-ERR timeout is out of range\r\n")

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestBLPopInvalidArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "BLPOP")
	expectReply(t, conn, "-ERR wrong number of arguments for 'blpop' command\r\n")
	do(t, conn, "BLPOP", "list_key")
	expectReply(t, conn, "-ERR wrong number of arguments for 'blpop' command\r\n")

	do(t, conn, "BLPOP", "list_key", "soon")
	expectReply(t, conn, "-ERR timeout is not a float or out of range\r\n")
	do(t, conn, "BLPOP", "list_key", "")
	expectReply(t, conn, "-ERR timeout is not a float or out of range\r\n")
	do(t, conn, "BLPOP", "list_key", "inf")
	expectReply(t, conn, "-ERR timeout is not a float or out of range\r\n")
	do(t, conn, "BLPOP", "list_key", "nan")
	expectReply(t, conn, "-ERR timeout is not a float or out of range\r\n")
	do(t, conn, "BLPOP", "list_key", "-1")
	expectReply(t, conn, "-ERR timeout is negative\r\n")

	// None of these left the client blocked.
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestBLPopAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "BLPOP", "text", "0")
	expectReply(t, conn, wrongTypeError)

	// Keys are examined in order: a list with elements that comes first wins.
	do(t, conn, "RPUSH", "list_key", "element")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "BLPOP", "list_key", "text", "0")
	expectReply(t, conn, bulkArray("list_key", "element"))
	do(t, conn, "BLPOP", "list_key", "text", "0")
	expectReply(t, conn, wrongTypeError)
}

func TestBLPopIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "blpop", "list_key", "0")
	expectReply(t, conn, bulkArray("list_key", "a"))
	do(t, conn, "BlPop", "list_key", "0")
	expectReply(t, conn, bulkArray("list_key", "b"))
}

func TestBLPopReturnsElementVerbatim(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	blocked := dial(t, addr)
	pusher := dial(t, addr)

	for _, element := range []string{"", "with space", "a\r\nb", "\x00\xff"} {
		do(t, blocked, "BLPOP", "a key", "0")
		waitForBlockedClients(t, srv, "a key", 1)
		do(t, pusher, "RPUSH", "a key", element)
		expectReply(t, pusher, ":1\r\n")
		expectReply(t, blocked, bulkArray("a key", element))
	}
}

// TestBLPopProducersAndConsumers runs several blocking consumers against
// several producers and checks that every element is delivered exactly once.
func TestBLPopProducersAndConsumers(t *testing.T) {
	addr := startTestServer(t)

	const consumers, producers, perProducer = 5, 4, 50
	total := producers * perProducer

	var (
		mu       sync.Mutex
		received []string
		wg       sync.WaitGroup
		errs     = make(chan error, consumers+producers)
		done     = make(chan struct{})
	)

	for i := 0; i < consumers; i++ {
		conn := dial(t, addr)
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reader := bufio.NewReader(conn)
			for {
				if _, err := conn.Write([]byte(encodeCommand("BLPOP", "queue", "0"))); err != nil {
					errs <- fmt.Errorf("consumer %d: write: %w", id, err)
					return
				}
				// Reply: *2, $5, queue, $<n>, <element>.
				var lines [5]string
				for n := range lines {
					line, err := reader.ReadString('\n')
					if err != nil {
						select {
						case <-done: // closed by the test once everything arrived
						default:
							errs <- fmt.Errorf("consumer %d: read: %w", id, err)
						}
						return
					}
					lines[n] = line
				}
				if lines[0] != "*2\r\n" || lines[2] != "queue\r\n" {
					errs <- fmt.Errorf("consumer %d: unexpected reply %q", id, lines)
					return
				}
				mu.Lock()
				received = append(received, lines[4][:len(lines[4])-2])
				finished := len(received) == total
				mu.Unlock()
				if finished {
					close(done)
				}
			}
		}(i)
		// Unblock the consumers that are still waiting when the test is over.
		defer conn.Close()
	}

	var want []string
	for i := 0; i < producers; i++ {
		conn := dial(t, addr)
		for n := 0; n < perProducer; n++ {
			want = append(want, fmt.Sprintf("producer-%d-element-%d", i, n))
		}
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reader := bufio.NewReader(conn)
			for n := 0; n < perProducer; n++ {
				element := fmt.Sprintf("producer-%d-element-%d", id, n)
				if _, err := conn.Write([]byte(encodeCommand("RPUSH", "queue", element))); err != nil {
					errs <- fmt.Errorf("producer %d: write: %w", id, err)
					return
				}
				if _, err := reader.ReadString('\n'); err != nil {
					errs <- fmt.Errorf("producer %d: read: %w", id, err)
					return
				}
			}
		}(i)
	}

	select {
	case <-done:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		mu.Lock()
		t.Fatalf("only %d of %d elements were delivered", len(received), total)
		mu.Unlock()
	}

	mu.Lock()
	got := append([]string(nil), received...)
	mu.Unlock()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered elements differ from the pushed ones (%d delivered, %d pushed)", len(got), len(want))
	}
}

func TestListWaiterQueues(t *testing.T) {
	srv := NewServer()

	first := srv.addListWaiter([]string{"a", "b", "a"})
	second := srv.addListWaiter([]string{"a"})

	if want := []string{"a", "b"}; !reflect.DeepEqual(first.keys, want) {
		t.Fatalf("keys = %q, want %q (duplicates removed)", first.keys, want)
	}
	if got := srv.listWaiters["a"]; len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("waiters on a are not [first second]")
	}

	// Serving a key with no list does nothing.
	srv.serveListWaiters("a")
	if len(srv.listWaiters["a"]) != 2 {
		t.Fatalf("waiters were removed although there was nothing to pop")
	}

	// One element: only the first waiter is served, and it leaves every queue.
	l, _ := srv.store.getOrCreateList("a")
	l.pushBack("x")
	srv.serveListWaiters("a")

	select {
	case got := <-first.served:
		if want := (poppedElement{key: "a", element: "x"}); got != want {
			t.Fatalf("first waiter got %+v, want %+v", got, want)
		}
	default:
		t.Fatalf("first waiter was not served")
	}
	select {
	case got := <-second.served:
		t.Fatalf("second waiter was served %+v, but there was a single element", got)
	default:
	}
	if got := srv.listWaiters["a"]; len(got) != 1 || got[0] != second {
		t.Fatalf("waiters on a are not [second] after serving first")
	}
	if _, waiting := srv.listWaiters["b"]; waiting {
		t.Fatalf("first waiter is still queued on b after being served through a")
	}
	if l, _ := srv.store.getList("a"); l != nil {
		t.Fatalf("emptied list was not deleted")
	}

	srv.removeListWaiter(second)
	if len(srv.listWaiters) != 0 {
		t.Fatalf("empty queues are kept around: %v", srv.listWaiters)
	}
}
