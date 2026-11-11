package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// subscribe connects a new client to the server at addr, subscribes it to
// the channels, which must all be different, and returns its connection.
func subscribe(t *testing.T, addr string, channels ...string) net.Conn {
	t.Helper()

	conn := dial(t, addr)
	do(t, conn, append([]string{"SUBSCRIBE"}, channels...)...)
	for i, channel := range channels {
		expectReply(t, conn, subscribed(channel, i+1))
	}
	return conn
}

// publish publishes a message to a channel on a connection of its own, like
// redis-cli does, and checks the number of subscribers the server replies
// with.
func publish(t *testing.T, addr, channel, message string, subscribers int) {
	t.Helper()

	conn := dial(t, addr)
	do(t, conn, "PUBLISH", channel, message)
	expectReply(t, conn, ":"+strconv.Itoa(subscribers)+"\r\n")
}

// waitForSubscribers waits until exactly n clients are subscribed to channel
// on srv. The server only learns that a client is gone some time after the
// client closed its connection.
func waitForSubscribers(t *testing.T, srv *Server, channel string, n int) {
	t.Helper()

	eventually(t, fmt.Sprintf("%d clients to be subscribed to %q", n, channel), func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.subscribers[channel]) == n
	})
}

func TestPublishRepliesWithTheNumberOfSubscribers(t *testing.T) {
	addr := startTestServer(t)
	subscribe(t, addr, "foo")
	subscribe(t, addr, "bar")
	subscribe(t, addr, "bar")

	conn := dial(t, addr)
	do(t, conn, "PUBLISH", "bar", "msg")
	expectReply(t, conn, ":2\r\n")
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, ":1\r\n")
}

func TestPublishToAChannelWithoutSubscribers(t *testing.T) {
	addr := startTestServer(t)

	// Nobody is subscribed to anything.
	publish(t, addr, "foo", "msg", 0)

	// Somebody is, but to another channel.
	subscribe(t, addr, "bar")
	publish(t, addr, "foo", "msg", 0)
	publish(t, addr, "bar", "msg", 1)
}

func TestPublishCountsEachSubscriberOnce(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	// Subscribing again to a channel doesn't make a client count twice.
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	do(t, conn, "SUBSCRIBE", "foo", "foo")
	expectReply(t, conn, subscribed("foo", 1)+subscribed("foo", 1))

	publish(t, addr, "foo", "msg", 1)
}

func TestPublishCountsTheSubscribersOfTheChannelOnly(t *testing.T) {
	addr := startTestServer(t)

	// A client counts for each of the channels it is subscribed to, however
	// it subscribed to them.
	subscribe(t, addr, "foo", "bar", "baz")
	subscribe(t, addr, "bar")
	conn := subscribe(t, addr, "baz")
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, subscribed("bar", 2))

	publish(t, addr, "foo", "msg", 1)
	publish(t, addr, "bar", "msg", 3)
	publish(t, addr, "baz", "msg", 2)
	publish(t, addr, "qux", "msg", 0)
}

func TestPublishToRandomChannels(t *testing.T) {
	addr := startTestServer(t)

	// Neither the names nor the counts can be hardcoded.
	for subscribers := range 4 {
		channel := randomWord(t)
		for range subscribers {
			subscribe(t, addr, channel)
		}
		publish(t, addr, channel, randomWord(t), subscribers)
	}
}

func TestPublishChannelNamesAreExact(t *testing.T) {
	addr := startTestServer(t)
	subscribe(t, addr, "news")

	// A channel is named in full: there is no ignoring the case, and nothing
	// is a pattern.
	for _, channel := range []string{"News", "NEWS", "news ", " news", "new", "newss", "n*", "*", "news*", "[n]ews", "ne?s", ""} {
		publish(t, addr, channel, "msg", 0)
	}
	publish(t, addr, "news", "msg", 1)

	// A name made of glob characters is a name like any other.
	subscribe(t, addr, "n*")
	publish(t, addr, "n*", "msg", 1)
	publish(t, addr, "news", "msg", 1)
}

func TestPublishAnyMessage(t *testing.T) {
	addr := startTestServer(t)
	subscribe(t, addr, "foo")

	for _, message := range []string{"", "hello world", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n", strings.Repeat("x", 100000)} {
		publish(t, addr, "foo", message, 1)
	}
}

func TestPublishWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for _, command := range [][]string{{"PUBLISH"}, {"PUBLISH", "foo"}, {"PUBLISH", "foo", "msg", "extra"}} {
		do(t, conn, command...)
		expectReply(t, conn, "-ERR wrong number of arguments for 'publish' command\r\n")
	}

	// The connection stays usable.
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, ":0\r\n")
}

func TestPublishCommandNameIsCaseInsensitive(t *testing.T) {
	addr := startTestServer(t)
	subscribe(t, addr, "MyChan")

	conn := dial(t, addr)
	for _, name := range []string{"PUBLISH", "publish", "PubLish"} {
		do(t, conn, name, "MyChan", "msg")
		expectReply(t, conn, ":1\r\n")
	}
}

func TestDisconnectedSubscribersAreNotCounted(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	first := subscribe(t, addr, "foo", "bar")
	second := subscribe(t, addr, "foo")
	publish(t, addr, "foo", "msg", 2)
	publish(t, addr, "bar", "msg", 1)

	first.Close()
	waitForSubscribers(t, srv, "foo", 1)
	publish(t, addr, "foo", "msg", 1)
	publish(t, addr, "bar", "msg", 0)

	second.Close()
	waitForSubscribers(t, srv, "foo", 0)
	publish(t, addr, "foo", "msg", 0)

	// Nothing is kept about the channels nobody is subscribed to anymore.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.subscribers) != 0 {
		t.Errorf("the server still knows about %d channels after every subscriber left, want 0", len(srv.subscribers))
	}
}

func TestSubscribersCannotPublish(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	// PUBLISH isn't one of the commands of subscribed mode.
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, notAllowedInSubscribedMode("publish"))

	// Any other client can publish to the channel.
	publish(t, addr, "foo", "msg", 1)
}

func TestPublishInATransaction(t *testing.T) {
	addr := startTestServer(t)
	subscribe(t, addr, "foo")
	conn := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "PUBLISH", "bar", "msg")
	expectReply(t, conn, "+QUEUED\r\n")

	// The message is published when the transaction is executed, to those
	// who are subscribed by then.
	subscribe(t, addr, "foo")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":2\r\n", ":0\r\n"))
}

func TestPublishIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	subscribe(t, addr, "foo")

	conn := dial(t, addr)
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "PUBLISH", "bar", "msg")
	expectReply(t, conn, ":0\r\n")
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")

	// Messages aren't part of the dataset: publishing logs nothing, and
	// creates no key.
	if got, want := readFile(t, incrPath), encodeCommand("SET", "key", "value"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	do(t, conn, "KEYS", "*")
	expectReply(t, conn, bulkArray("key"))
	do(t, conn, "TYPE", "foo")
	expectReply(t, conn, "+none\r\n")
}

func TestPublishWhileSubscribersComeAndGo(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)

	const subscribers, publishers, messages = 20, 5, 50
	errs := make(chan error, subscribers+publishers)
	var wg sync.WaitGroup

	// Subscribers join, stay for a short while and leave.
	for id := range subscribers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", addr, replyTimeout)
			if err != nil {
				errs <- fmt.Errorf("subscriber %d: dial: %w", id, err)
				return
			}
			defer conn.Close()

			want := subscribed("foo", 1)
			if _, err := conn.Write([]byte(encodeCommand("SUBSCRIBE", "foo"))); err != nil {
				errs <- fmt.Errorf("subscriber %d: write: %w", id, err)
				return
			}
			got := make([]byte, len(want))
			conn.SetReadDeadline(time.Now().Add(replyTimeout))
			if _, err := io.ReadFull(conn, got); err != nil {
				errs <- fmt.Errorf("subscriber %d: read: %w", id, err)
				return
			}
			if string(got) != want {
				errs <- fmt.Errorf("subscriber %d: reply = %q, want %q", id, got, want)
				return
			}
			time.Sleep(time.Duration(id) * time.Millisecond)
		}()
	}

	// Meanwhile, publishers are told how many subscribers there are: never
	// more than the number of clients that ever subscribed.
	for id := range publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			conn, err := net.DialTimeout("tcp", addr, replyTimeout)
			if err != nil {
				errs <- fmt.Errorf("publisher %d: dial: %w", id, err)
				return
			}
			defer conn.Close()

			r := bufio.NewReader(conn)
			for n := range messages {
				if _, err := conn.Write([]byte(encodeCommand("PUBLISH", "foo", strconv.Itoa(n)))); err != nil {
					errs <- fmt.Errorf("publisher %d: write: %w", id, err)
					return
				}
				conn.SetReadDeadline(time.Now().Add(replyTimeout))
				line, err := r.ReadString('\n')
				if err != nil {
					errs <- fmt.Errorf("publisher %d: read: %w", id, err)
					return
				}
				count, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line, ":"), "\r\n"))
				if err != nil || !strings.HasPrefix(line, ":") || count < 0 || count > subscribers {
					errs <- fmt.Errorf("publisher %d: reply = %q, want an integer between 0 and %d", id, line, subscribers)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Once everybody has left, nobody is counted anymore.
	waitForSubscribers(t, srv, "foo", 0)
	publish(t, addr, "foo", "msg", 0)
}
