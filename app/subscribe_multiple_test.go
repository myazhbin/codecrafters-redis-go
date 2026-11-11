package main

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestSubscribeToMultipleChannels(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "foo")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nfoo\r\n:1\r\n")
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nbar\r\n:2\r\n")
	// The client is subscribed to bar already: the count stays the same.
	do(t, conn, "SUBSCRIBE", "bar")
	expectReply(t, conn, "*3\r\n$9\r\nsubscribe\r\n$3\r\nbar\r\n:2\r\n")
}

func TestSubscribingAgainToAChannelDoesNotCountTwice(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for range 3 {
		do(t, conn, "SUBSCRIBE", "channel_1")
		expectReply(t, conn, subscribed("channel_1", 1))
	}

	// Channels that were subscribed to earlier don't count twice either.
	do(t, conn, "SUBSCRIBE", "channel_2")
	expectReply(t, conn, subscribed("channel_2", 2))
	do(t, conn, "SUBSCRIBE", "channel_1")
	expectReply(t, conn, subscribed("channel_1", 2))
	do(t, conn, "SUBSCRIBE", "channel_3")
	expectReply(t, conn, subscribed("channel_3", 3))
	do(t, conn, "SUBSCRIBE", "channel_2")
	expectReply(t, conn, subscribed("channel_2", 3))
}

func TestSubscriptionCountsArePerClient(t *testing.T) {
	addr := startTestServer(t)
	first, second, third := dial(t, addr), dial(t, addr), dial(t, addr)

	// The clients subscribe in turns, some of them to the same channels:
	// each one is told how many channels it is subscribed to itself.
	do(t, first, "SUBSCRIBE", "foo")
	expectReply(t, first, subscribed("foo", 1))
	do(t, second, "SUBSCRIBE", "foo")
	expectReply(t, second, subscribed("foo", 1))
	do(t, first, "SUBSCRIBE", "bar")
	expectReply(t, first, subscribed("bar", 2))
	do(t, third, "SUBSCRIBE", "baz")
	expectReply(t, third, subscribed("baz", 1))
	do(t, first, "SUBSCRIBE", "baz")
	expectReply(t, first, subscribed("baz", 3))
	do(t, second, "SUBSCRIBE", "foo")
	expectReply(t, second, subscribed("foo", 1))
	do(t, second, "SUBSCRIBE", "bar")
	expectReply(t, second, subscribed("bar", 2))
	do(t, third, "SUBSCRIBE", "baz")
	expectReply(t, third, subscribed("baz", 1))
	do(t, first, "SUBSCRIBE", "foo")
	expectReply(t, first, subscribed("foo", 3))
}

func TestSubscribeToRandomChannels(t *testing.T) {
	addr := startTestServer(t)

	for range 3 {
		conn := dial(t, addr)
		one, two := randomWord(t), randomWord(t)
		do(t, conn, "SUBSCRIBE", one)
		expectReply(t, conn, subscribed(one, 1))
		do(t, conn, "SUBSCRIBE", two)
		expectReply(t, conn, subscribed(two, 2))
		do(t, conn, "SUBSCRIBE", two)
		expectReply(t, conn, subscribed(two, 2))
		do(t, conn, "SUBSCRIBE", one)
		expectReply(t, conn, subscribed(one, 2))
	}
}

func TestSubscribeToSeveralChannelsAtOnce(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// Each channel gets its own reply, with the count as it is at that point.
	do(t, conn, "SUBSCRIBE", "a", "b", "c")
	expectReply(t, conn, subscribed("a", 1)+subscribed("b", 2)+subscribed("c", 3))

	// A channel that is given twice, or that the client is subscribed to
	// already, is confirmed again without being counted again.
	do(t, conn, "SUBSCRIBE", "d", "d", "a", "e")
	expectReply(t, conn, subscribed("d", 4)+subscribed("d", 4)+subscribed("a", 4)+subscribed("e", 5))
}

func TestChannelNamesAreCaseSensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SUBSCRIBE", "news")
	expectReply(t, conn, subscribed("news", 1))
	do(t, conn, "SUBSCRIBE", "News")
	expectReply(t, conn, subscribed("News", 2))
	do(t, conn, "SUBSCRIBE", "NEWS")
	expectReply(t, conn, subscribed("NEWS", 3))
	// And so are names that only differ by a space.
	do(t, conn, "SUBSCRIBE", "news ")
	expectReply(t, conn, subscribed("news ", 4))
	do(t, conn, "SUBSCRIBE", "news")
	expectReply(t, conn, subscribed("news", 4))
}

func TestSubscribeToManyChannels(t *testing.T) {
	conn := dial(t, startTestServer(t))

	const channels = 200
	for i := 1; i <= channels; i++ {
		channel := "channel_" + strconv.Itoa(i)
		do(t, conn, "SUBSCRIBE", channel)
		expectReply(t, conn, subscribed(channel, i))
	}
	for i := 1; i <= channels; i++ {
		channel := "channel_" + strconv.Itoa(i)
		do(t, conn, "SUBSCRIBE", channel)
		expectReply(t, conn, subscribed(channel, channels))
	}
}

func TestPipelinedSubscriptions(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// The commands arrive in a single write; the replies come in order.
	send(t, conn, encodeCommand("SUBSCRIBE", "foo")+encodeCommand("SUBSCRIBE", "bar")+encodeCommand("SUBSCRIBE", "foo"))
	expectReply(t, conn, subscribed("foo", 1)+subscribed("bar", 2)+subscribed("foo", 2))
}

func TestSubscriptionsEndWithTheConnection(t *testing.T) {
	addr := startTestServer(t)

	conn := dial(t, addr)
	do(t, conn, "SUBSCRIBE", "foo", "bar")
	expectReply(t, conn, subscribed("foo", 1)+subscribed("bar", 2))
	conn.Close()

	// Another connection is another client, which starts from nothing.
	conn = dial(t, addr)
	do(t, conn, "SUBSCRIBE", "baz")
	expectReply(t, conn, subscribed("baz", 1))
}

func TestClientsSubscribeConcurrently(t *testing.T) {
	addr := startTestServer(t)

	const clients, channels = 10, 20
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	// Every client subscribes to the same channels at the same time, and
	// must only ever see its own count.
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for id, conn := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= channels; i++ {
				channel := "channel_" + strconv.Itoa(i)
				want := subscribed(channel, i)

				if _, err := conn.Write([]byte(encodeCommand("SUBSCRIBE", channel))); err != nil {
					errs <- fmt.Errorf("client %d: write: %w", id, err)
					return
				}
				got := make([]byte, len(want))
				conn.SetReadDeadline(time.Now().Add(replyTimeout))
				if _, err := io.ReadFull(conn, got); err != nil {
					errs <- fmt.Errorf("client %d: read: %w", id, err)
					return
				}
				if string(got) != want {
					errs <- fmt.Errorf("client %d: reply = %q, want %q", id, got, want)
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
}

func TestSubscriptionsInATransaction(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	for _, channel := range []string{"foo", "bar", "foo"} {
		do(t, conn, "SUBSCRIBE", channel)
		expectReply(t, conn, "+QUEUED\r\n")
	}
	// The subscriptions add up as the transaction is executed.
	do(t, conn, "EXEC")
	expectReply(t, conn, array(subscribed("foo", 1), subscribed("bar", 2), subscribed("foo", 2)))

	do(t, conn, "SUBSCRIBE", "baz")
	expectReply(t, conn, subscribed("baz", 3))
}
