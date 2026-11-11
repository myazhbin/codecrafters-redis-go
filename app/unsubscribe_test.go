package main

import (
	"bufio"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// unsubscribed returns the RESP encoding of what a client receives when it
// has unsubscribed from a channel: an array holding "unsubscribe", the
// channel and the number of channels the client is still subscribed to.
func unsubscribed(channel string, count int) string {
	return array(bulk("unsubscribe"), bulk(channel), ":"+strconv.Itoa(count)+"\r\n")
}

func TestUnsubscribe(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, subscribed("bar", 2))

	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, "*3\r\n$11\r\nunsubscribe\r\n$3\r\nfoo\r\n:1\r\n")
	do(t, conn, "UNSUBSCRIBE", "bar")
	expectReply(t, conn, "*3\r\n$11\r\nunsubscribe\r\n$3\r\nbar\r\n:0\r\n")
}

func TestUnsubscribeFromAChannelTheClientIsNotSubscribedTo(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")

	// The reply is the usual one, and the count doesn't change.
	do(t, conn, "UNSUBSCRIBE", "bar")
	expectReply(t, conn, "*3\r\n$11\r\nunsubscribe\r\n$3\r\nbar\r\n:1\r\n")

	// Nothing happened to the subscription the client does have.
	publish(t, addr, "foo", "hello", 1)
	expectReply(t, conn, message("foo", "hello"))
}

func TestUnsubscribedClientStopsReceivingMessages(t *testing.T) {
	addr := startTestServer(t)
	first := subscribe(t, addr, "foo", "baz")
	second := subscribe(t, addr, "foo", "bar")
	publisher := dial(t, addr)

	do(t, publisher, "PUBLISH", "foo", "before-unsubscribe")
	expectReply(t, publisher, ":2\r\n")
	expectReply(t, first, message("foo", "before-unsubscribe"))
	expectReply(t, second, message("foo", "before-unsubscribe"))

	do(t, first, "UNSUBSCRIBE", "foo")
	expectReply(t, first, unsubscribed("foo", 1))

	// The channel has one subscriber less, which doesn't get its messages
	// anymore.
	do(t, publisher, "PUBLISH", "foo", "after-unsubscribe")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, second, message("foo", "after-unsubscribe"))
	expectNoReply(t, first, 50*time.Millisecond)

	// The other subscriptions of the client are still there.
	do(t, publisher, "PUBLISH", "baz", "still-subscribed")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, first, message("baz", "still-subscribed"))
	do(t, publisher, "PUBLISH", "bar", "for-the-second")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, second, message("bar", "for-the-second"))
	expectNoReply(t, first, 50*time.Millisecond)
}

func TestUnsubscribeFromRandomChannels(t *testing.T) {
	addr := startTestServer(t)

	// The replies can't be hardcoded.
	for range 3 {
		one, two := randomWord(t), randomWord(t)
		conn := subscribe(t, addr, one, two)
		do(t, conn, "UNSUBSCRIBE", two)
		expectReply(t, conn, unsubscribed(two, 1))
		do(t, conn, "UNSUBSCRIBE", randomWord(t)+"-never-subscribed")
		if got := readReplyLine(t, conn); got != "*3" {
			t.Fatalf("reply starts with %q, want an array of 3 elements", got)
		}
		drainPendingReply(t, conn)
		do(t, conn, "UNSUBSCRIBE", one)
		expectReply(t, conn, unsubscribed(one, 0))
	}
}

func TestUnsubscribeTwice(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo", "bar")

	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 1))
	// The second time, the client isn't subscribed to the channel anymore.
	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 1))
	do(t, conn, "UNSUBSCRIBE", "bar")
	expectReply(t, conn, unsubscribed("bar", 0))
	do(t, conn, "UNSUBSCRIBE", "bar")
	expectReply(t, conn, unsubscribed("bar", 0))
}

func TestUnsubscribeFromSeveralChannelsAtOnce(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "a", "b", "c", "d")

	// Each channel gets its own reply, with the count as it is at that point,
	// whether the client was subscribed to it or not.
	do(t, conn, "UNSUBSCRIBE", "b", "nosuch", "a", "b")
	expectReply(t, conn, unsubscribed("b", 3)+unsubscribed("nosuch", 3)+unsubscribed("a", 2)+unsubscribed("b", 2))

	publish(t, addr, "a", "msg", 0)
	publish(t, addr, "b", "msg", 0)
	publish(t, addr, "c", "msg", 1)
	publish(t, addr, "d", "msg", 1)
	expectReply(t, conn, message("c", "msg")+message("d", "msg"))
}

func TestUnsubscribeFromEverything(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	channels := []string{"news", "weather", "sport", "a channel", ""}
	conn := subscribe(t, addr, channels...)
	other := subscribe(t, addr, "news")

	// Without channels, UNSUBSCRIBE ends every subscription of the client.
	// There is one reply per channel, in an order that isn't specified, and
	// the count goes down to 0.
	do(t, conn, "UNSUBSCRIBE")
	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	r := bufio.NewReader(conn)
	var got []string
	for remaining := len(channels) - 1; remaining >= 0; remaining-- {
		push, err := readPush(r)
		if err != nil {
			t.Fatalf("reading the reply for a channel, %d to go: %v", remaining+1, err)
		}
		if len(push) != 3 || push[0] != "unsubscribe" || push[2] != strconv.Itoa(remaining) {
			t.Fatalf("received %q, want [unsubscribe <channel> %d]", push, remaining)
		}
		got = append(got, push[1])
	}
	want := append([]string(nil), channels...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("unsubscribed from %q, want %q", got, want)
	}

	// The client gets nothing anymore, and the others still do.
	publish(t, addr, "news", "msg", 1)
	publish(t, addr, "weather", "msg", 0)
	publish(t, addr, "", "msg", 0)
	expectReply(t, other, message("news", "msg"))
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if b, err := r.Peek(1); err == nil {
		t.Errorf("the client still receives data after unsubscribing from everything, starting with %q", b)
	}

	// The server only remembers the channels somebody is subscribed to.
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.subscribers) != 1 || len(srv.subscribers["news"]) != 1 {
		t.Errorf("the server knows about %d channels, want only news with 1 subscriber", len(srv.subscribers))
	}
}

func TestUnsubscribeWithoutAnySubscription(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// There is still a reply, in which nothing stands for the channel.
	do(t, conn, "UNSUBSCRIBE")
	expectReply(t, conn, "*3\r\n$11\r\nunsubscribe\r\n$-1\r\n:0\r\n")
	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 0))
	do(t, conn, "UNSUBSCRIBE", "foo", "bar")
	expectReply(t, conn, unsubscribed("foo", 0)+unsubscribed("bar", 0))

	// None of that puts the client in subscribed mode.
	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, bulk("hey"))
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")

	// The same goes for a client that has ended all of its subscriptions.
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	do(t, conn, "UNSUBSCRIBE")
	expectReply(t, conn, unsubscribed("foo", 0))
	do(t, conn, "UNSUBSCRIBE")
	expectReply(t, conn, "*3\r\n$11\r\nunsubscribe\r\n$-1\r\n:0\r\n")
}

func TestClientLeavesSubscribedModeWithItsLastSubscription(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo", "bar")

	// As long as one subscription is left, the client is in subscribed mode.
	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 1))
	do(t, conn, "GET", "key")
	expectReply(t, conn, notAllowedInSubscribedMode("get"))
	do(t, conn, "PING")
	expectReply(t, conn, subscribedPong(""))
	do(t, conn, "UNSUBSCRIBE", "nosuch")
	expectReply(t, conn, unsubscribed("nosuch", 1))
	do(t, conn, "GET", "key")
	expectReply(t, conn, notAllowedInSubscribedMode("get"))

	// With the last one gone, it can send any command again.
	do(t, conn, "UNSUBSCRIBE", "bar")
	expectReply(t, conn, unsubscribed("bar", 0))
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "GET", "key")
	expectReply(t, conn, bulk("value"))
	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "counter")
	expectReply(t, conn, "+QUEUED\r\n")
	do(t, conn, "EXEC")
	expectReply(t, conn, array(":1\r\n"))
	do(t, conn, "PUBLISH", "foo", "msg")
	expectReply(t, conn, ":0\r\n")

	// And it is back in subscribed mode as soon as it subscribes again.
	do(t, conn, "SUBSCRIBE", "baz")
	expectReply(t, conn, subscribed("baz", 1))
	do(t, conn, "GET", "key")
	expectReply(t, conn, notAllowedInSubscribedMode("get"))
	do(t, conn, "PING")
	expectReply(t, conn, subscribedPong(""))
}

func TestUnsubscribeOnlyConcernsTheClient(t *testing.T) {
	addr := startTestServer(t)
	leaving := subscribe(t, addr, "foo")
	staying := subscribe(t, addr, "foo")

	do(t, leaving, "UNSUBSCRIBE", "foo")
	expectReply(t, leaving, unsubscribed("foo", 0))
	// A client can't end the subscription of another one.
	do(t, leaving, "UNSUBSCRIBE", "foo")
	expectReply(t, leaving, unsubscribed("foo", 0))

	publish(t, addr, "foo", "hello", 1)
	expectReply(t, staying, message("foo", "hello"))
	expectNoReply(t, leaving, 50*time.Millisecond)

	do(t, staying, "PING")
	expectReply(t, staying, subscribedPong(""))
	do(t, leaving, "PING")
	expectReply(t, leaving, "+PONG\r\n")
}

func TestUnsubscribeNamesAreExact(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "news")

	// The command's name is case-insensitive; the channel's isn't, and
	// nothing in it is a pattern.
	for i, channel := range []string{"News", "NEWS", "news ", "new", "n*", "*", "ne?s", ""} {
		name := []string{"UNSUBSCRIBE", "unsubscribe", "UnSubscribe"}[i%3]
		do(t, conn, name, channel)
		expectReply(t, conn, unsubscribed(channel, 1))
	}
	publish(t, addr, "news", "still subscribed", 1)
	expectReply(t, conn, message("news", "still subscribed"))

	do(t, conn, "unsubscribe", "news")
	expectReply(t, conn, unsubscribed("news", 0))
	publish(t, addr, "news", "not anymore", 0)
}

func TestUnsubscribeFromBinarySafeChannelNames(t *testing.T) {
	addr := startTestServer(t)

	for _, channel := range []string{"", "my  channel", "a\r\nb", "a\x00b", "chaîne ✓", "*1\r\n$4\r\nPING\r\n"} {
		conn := subscribe(t, addr, channel, "other")
		do(t, conn, "UNSUBSCRIBE", channel)
		expectReply(t, conn, unsubscribed(channel, 1))
		publish(t, addr, channel, "msg", 0)
		conn.Close()
	}
}

func TestSubscribeAgainAfterUnsubscribing(t *testing.T) {
	addr := startTestServer(t)
	conn := subscribe(t, addr, "foo")
	publish(t, addr, "foo", "one", 1)
	expectReply(t, conn, message("foo", "one"))

	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 0))
	publish(t, addr, "foo", "missed", 0)

	// The subscription starts over: what was published in between is lost.
	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, subscribed("foo", 1))
	publish(t, addr, "foo", "two", 1)
	expectReply(t, conn, message("foo", "two"))
	expectNoReply(t, conn, 50*time.Millisecond)
}

func TestUnsubscribeInATransaction(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, command := range [][]string{{"SUBSCRIBE", "foo"}, {"SUBSCRIBE", "bar"}, {"UNSUBSCRIBE", "foo"}, {"PING"}, {"UNSUBSCRIBE", "bar"}, {"PING"}} {
		do(t, conn, command...)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// The client is in and out of subscribed mode within the transaction.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(subscribed("foo", 1), subscribed("bar", 2), unsubscribed("foo", 1),
		subscribedPong(""), unsubscribed("bar", 0), "+PONG\r\n"))

	do(t, conn, "ECHO", "hey")
	expectReply(t, conn, bulk("hey"))
	publish(t, addr, "foo", "msg", 0)
	publish(t, addr, "bar", "msg", 0)
}

func TestUnsubscribeIsNotAWrite(t *testing.T) {
	dir, incrPath := prepareAOFDir(t, "appendonlydir", "appendonly.aof", "custom.1.incr.aof")
	srv := newServerWithAOF(t, "--dir", dir, "--appendonly", "yes", "--appendfsync", "always")
	addr := startServer(t, srv)
	replica := connectReplica(t, addr)

	conn := subscribe(t, addr, "foo")
	do(t, conn, "UNSUBSCRIBE", "foo")
	expectReply(t, conn, unsubscribed("foo", 0))
	do(t, conn, "SET", "key", "value")
	expectReply(t, conn, "+OK\r\n")

	// The write is all there is in the log and in the replication stream.
	if got, want := readFile(t, incrPath), encodeCommand("SET", "key", "value"); got != want {
		t.Errorf("append-only file holds %q, want %q", got, want)
	}
	expectPropagated(t, replica, "SET", "key", "value")
	expectNoReply(t, replica, 50*time.Millisecond)
}

func TestNoMessageAfterTheUnsubscriptionIsConfirmed(t *testing.T) {
	addr := startTestServer(t)

	// Publishers send messages to the channel without pause...
	stop := make(chan struct{})
	var publishers sync.WaitGroup
	errs := make(chan error, 64)
	for id := range 4 {
		publishers.Add(1)
		go func() {
			defer publishers.Done()

			conn, err := net.DialTimeout("tcp", addr, replyTimeout)
			if err != nil {
				errs <- fmt.Errorf("publisher %d: dial: %w", id, err)
				return
			}
			defer conn.Close()
			r := bufio.NewReader(conn)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := conn.Write([]byte(encodeCommand("PUBLISH", "foo", "flood"))); err != nil {
					errs <- fmt.Errorf("publisher %d: write: %w", id, err)
					return
				}
				conn.SetReadDeadline(time.Now().Add(replyTimeout))
				if _, err := r.ReadString('\n'); err != nil {
					errs <- fmt.Errorf("publisher %d: read: %w", id, err)
					return
				}
			}
		}()
	}

	// ...while clients subscribe to it and unsubscribe from it. Whatever the
	// timing, the messages stop where the unsubscription is confirmed: the
	// next thing the client receives is the reply to its next command.
	for i := range 200 {
		conn, err := net.DialTimeout("tcp", addr, replyTimeout)
		if err != nil {
			t.Fatalf("subscriber %d: dial: %v", i, err)
		}
		request := encodeCommand("SUBSCRIBE", "foo") + encodeCommand("UNSUBSCRIBE", "foo") + encodeCommand("ECHO", "done")
		if _, err := conn.Write([]byte(request)); err != nil {
			t.Fatalf("subscriber %d: write: %v", i, err)
		}
		conn.SetReadDeadline(time.Now().Add(replyTimeout))
		r := bufio.NewReader(conn)

		first, err := readPush(r)
		if err != nil {
			t.Fatalf("subscriber %d: reading the confirmation: %v", i, err)
		}
		if got, want := strings.Join(first, " "), "subscribe foo 1"; got != want {
			t.Fatalf("subscriber %d: received %q first, want %q", i, got, want)
		}
		for {
			push, err := readPush(r)
			if err != nil {
				t.Fatalf("subscriber %d: waiting for the unsubscription to be confirmed: %v", i, err)
			}
			got := strings.Join(push, " ")
			if got == "unsubscribe foo 0" {
				break
			}
			if got != "message foo flood" {
				t.Fatalf("subscriber %d: received %q, want a message or the confirmation of the unsubscription", i, got)
			}
		}
		rest := make([]byte, len(bulk("done")))
		if _, err := r.Read(rest[:1]); err != nil {
			t.Fatalf("subscriber %d: reading the reply to ECHO: %v", i, err)
		}
		if rest[0] != '$' {
			line, _ := r.ReadString('\n')
			t.Fatalf("subscriber %d: received %q after the unsubscription was confirmed, want the reply to ECHO", i, string(rest[:1])+line)
		}
		conn.Close()
	}

	close(stop)
	publishers.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
