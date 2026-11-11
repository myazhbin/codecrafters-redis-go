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

// message returns the RESP encoding of what a subscriber of channel receives
// when payload is published to it: an array holding "message", the channel
// and the payload.
func message(channel, payload string) string {
	return bulkArray("message", channel, payload)
}

// readPush reads the next thing a subscriber is sent from r, which must be an
// array whose elements are bulk strings or integers, and returns its
// elements. Integers are returned in decimal.
func readPush(r *bufio.Reader) ([]string, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "*") || !strings.HasSuffix(header, "\r\n") {
		return nil, fmt.Errorf("received %q, want the start of an array", header)
	}
	count, err := strconv.Atoi(header[1 : len(header)-2])
	if err != nil || count < 0 {
		return nil, fmt.Errorf("invalid array header %q", header)
	}

	items := make([]string, 0, count)
	for range count {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(line, "\r\n") {
			return nil, fmt.Errorf("line %q isn't terminated by CRLF", line)
		}
		switch value := line[1 : len(line)-2]; line[0] {
		case ':':
			items = append(items, value)
		case '$':
			length, err := strconv.Atoi(value)
			if err != nil || length < 0 {
				return nil, fmt.Errorf("invalid bulk string header %q", line)
			}
			payload := make([]byte, length+2)
			if _, err := io.ReadFull(r, payload); err != nil {
				return nil, err
			}
			if string(payload[length:]) != "\r\n" {
				return nil, fmt.Errorf("bulk string %q isn't terminated by CRLF", payload)
			}
			items = append(items, string(payload[:length]))
		default:
			return nil, fmt.Errorf("unexpected array element starting with %q", line)
		}
	}
	return items, nil
}

func TestPublishedMessageIsDeliveredToSubscribers(t *testing.T) {
	addr := startTestServer(t)
	first := subscribe(t, addr, "foo")
	second := subscribe(t, addr, "foo")
	third := subscribe(t, addr, "bar")

	publisher := dial(t, addr)
	do(t, publisher, "PUBLISH", "foo", "hello")
	expectReply(t, publisher, ":2\r\n")
	expectReply(t, first, "*3\r\n$7\r\nmessage\r\n$3\r\nfoo\r\n$5\r\nhello\r\n")
	expectReply(t, second, "*3\r\n$7\r\nmessage\r\n$3\r\nfoo\r\n$5\r\nhello\r\n")

	do(t, publisher, "PUBLISH", "bar", "world")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, third, "*3\r\n$7\r\nmessage\r\n$3\r\nbar\r\n$5\r\nworld\r\n")

	// Each client got the messages of its channel, and nothing else: the
	// subscribers of foo didn't get what was published to bar, and the
	// subscriber of bar didn't get what was published to foo.
	for _, conn := range []net.Conn{first, second, third, publisher} {
		expectNoReply(t, conn, 50*time.Millisecond)
	}
}

func TestMessagesAreOnlyDeliveredToSubscribers(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")
	elsewhere := subscribe(t, addr, "bar")
	bystander := dial(t, addr)
	do(t, bystander, "PING")
	expectReply(t, bystander, "+PONG\r\n")

	publisher := dial(t, addr)
	do(t, publisher, "PUBLISH", "foo", "hello")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, subscriber, message("foo", "hello"))

	// Not the client subscribed to something else, not the one that isn't
	// subscribed to anything, and not the one that published the message.
	for _, conn := range []net.Conn{elsewhere, bystander, publisher} {
		expectNoReply(t, conn, 50*time.Millisecond)
	}

	// Their connections work as before.
	do(t, bystander, "ECHO", "hey")
	expectReply(t, bystander, bulk("hey"))
	do(t, publisher, "PUBLISH", "bar", "world")
	expectReply(t, publisher, ":1\r\n")
	expectReply(t, elsewhere, message("bar", "world"))
}

func TestRandomMessagesAreDelivered(t *testing.T) {
	addr := startTestServer(t)

	// Neither the channel nor the message can be hardcoded.
	for range 3 {
		channel, payload := randomWord(t), randomWord(t)
		subscriber := subscribe(t, addr, channel)
		publish(t, addr, channel, payload, 1)
		expectReply(t, subscriber, message(channel, payload))
	}
}

func TestMessagesAreDeliveredInTheOrderTheyWerePublished(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")
	publisher := dial(t, addr)

	const messages = 200
	var want strings.Builder
	for i := range messages {
		do(t, publisher, "PUBLISH", "foo", "message-"+strconv.Itoa(i))
		expectReply(t, publisher, ":1\r\n")
		want.WriteString(message("foo", "message-"+strconv.Itoa(i)))
	}
	expectReply(t, subscriber, want.String())
}

func TestSubscriberOfSeveralChannelsGetsTheMessagesOfEachOfThem(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "news", "weather")
	// A channel that is subscribed to later is no different.
	do(t, subscriber, "SUBSCRIBE", "sport")
	expectReply(t, subscriber, subscribed("sport", 3))

	publish(t, addr, "weather", "rain", 1)
	publish(t, addr, "news", "something happened", 1)
	publish(t, addr, "other", "not for the subscriber", 0)
	publish(t, addr, "sport", "goal", 1)
	publish(t, addr, "news", "something else happened", 1)

	// Each message says which channel it comes from.
	expectReply(t, subscriber, message("weather", "rain")+
		message("news", "something happened")+
		message("sport", "goal")+
		message("news", "something else happened"))
	expectNoReply(t, subscriber, 50*time.Millisecond)
}

func TestMessageIsDeliveredOnceToAClientThatSubscribedTwice(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")
	do(t, subscriber, "SUBSCRIBE", "foo")
	expectReply(t, subscriber, subscribed("foo", 1))

	publish(t, addr, "foo", "hello", 1)
	expectReply(t, subscriber, message("foo", "hello"))
	expectNoReply(t, subscriber, 50*time.Millisecond)
}

func TestMessagesAreBinarySafe(t *testing.T) {
	addr := startTestServer(t)

	payloads := []string{"", "hello  big world", "a\r\nb", "a\x00b", "héllo ✓", "*1\r\n$4\r\nPING\r\n", "message",
		strings.Repeat("0123456789abcdef", 64*1024)}
	for _, channel := range []string{"foo", "my  channel", "a\r\nb", "chaîne ✓", ""} {
		subscriber := subscribe(t, addr, channel)
		for _, payload := range payloads {
			publish(t, addr, channel, payload, 1)
			expectReply(t, subscriber, message(channel, payload))
		}
		subscriber.Close()
	}
}

func TestNoMessageFromBeforeTheSubscription(t *testing.T) {
	addr := startTestServer(t)

	// Messages aren't kept: what is published while nobody listens is lost.
	publish(t, addr, "foo", "too early", 0)
	subscriber := subscribe(t, addr, "foo")
	publish(t, addr, "foo", "in time", 1)

	expectReply(t, subscriber, message("foo", "in time"))
	expectNoReply(t, subscriber, 50*time.Millisecond)
}

func TestManySubscribersAllGetTheMessage(t *testing.T) {
	addr := startTestServer(t)

	const subscribers = 100
	conns := make([]net.Conn, subscribers)
	for i := range conns {
		conns[i] = subscribe(t, addr, "foo")
	}

	publish(t, addr, "foo", "hello", subscribers)
	for _, conn := range conns {
		expectReply(t, conn, message("foo", "hello"))
	}
}

func TestPublishInATransactionIsDeliveredOnExec(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")
	publisher := dial(t, addr)

	// A transaction that is given up publishes nothing.
	do(t, publisher, "MULTI")
	expectReply(t, publisher, "+OK\r\n")
	do(t, publisher, "PUBLISH", "foo", "discarded")
	expectReply(t, publisher, "+QUEUED\r\n")
	do(t, publisher, "DISCARD")
	expectReply(t, publisher, "+OK\r\n")

	do(t, publisher, "MULTI")
	expectReply(t, publisher, "+OK\r\n")
	do(t, publisher, "PUBLISH", "foo", "first")
	expectReply(t, publisher, "+QUEUED\r\n")
	do(t, publisher, "PUBLISH", "foo", "second")
	expectReply(t, publisher, "+QUEUED\r\n")
	// Nothing is published as long as the commands are only queued.
	expectNoReply(t, subscriber, 50*time.Millisecond)

	do(t, publisher, "EXEC")
	expectReply(t, publisher, array(":1\r\n", ":1\r\n"))
	expectReply(t, subscriber, message("foo", "first")+message("foo", "second"))
}

func TestMessagesAfterARejectedCommand(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")

	// The replies to what a subscriber sends and the messages it is sent
	// follow one another on its connection.
	do(t, subscriber, "GET", "key")
	expectReply(t, subscriber, notAllowedInSubscribedMode("get"))
	publish(t, addr, "foo", "one", 1)
	expectReply(t, subscriber, message("foo", "one"))

	do(t, subscriber, "NOSUCHCOMMAND")
	expectReply(t, subscriber, "-ERR unknown command 'NOSUCHCOMMAND', with args beginning with: \r\n")
	do(t, subscriber, "PING")
	expectReply(t, subscriber, subscribedPong(""))
	publish(t, addr, "foo", "two", 1)
	expectReply(t, subscriber, message("foo", "two"))
}

func TestMessagesKeepFlowingAfterASubscriberLeaves(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	leaving := subscribe(t, addr, "foo")
	staying := subscribe(t, addr, "foo")

	publish(t, addr, "foo", "one", 2)
	expectReply(t, leaving, message("foo", "one"))
	expectReply(t, staying, message("foo", "one"))

	// Publishing to a client that has just gone is harmless, whether the
	// server knows that it is gone or not.
	leaving.Close()
	publisher := dial(t, addr)
	do(t, publisher, "PUBLISH", "foo", "two")
	if line := readReplyLine(t, publisher); line != ":1" && line != ":2" {
		t.Fatalf("reply to PUBLISH = %q, want :1 or :2", line)
	}
	expectReply(t, staying, message("foo", "two"))

	waitForSubscribers(t, srv, "foo", 1)
	publish(t, addr, "foo", "three", 1)
	expectReply(t, staying, message("foo", "three"))
}

func TestProtocolErrorFromASubscriberEndsItsSubscriptions(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	subscriber := subscribe(t, addr, "foo")

	// The server gives up on a connection it can't make sense of.
	send(t, subscriber, "*1\r\n+PING\r\n")
	subscriber.SetReadDeadline(time.Now().Add(replyTimeout))
	rest, err := io.ReadAll(subscriber)
	if err != nil {
		t.Fatalf("waiting for the server to close the connection: %v", err)
	}
	if got := string(rest); got != "" && got != "-ERR Protocol error: expected '$', got '+'\r\n" {
		t.Errorf("received %q before the connection was closed, want nothing but the protocol error", got)
	}

	waitForSubscribers(t, srv, "foo", 0)
	publish(t, addr, "foo", "hello", 0)
}

func TestSubscriptionIsConfirmedBeforeAnyMessage(t *testing.T) {
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

	// ...while clients subscribe to it: whatever the timing, the first thing
	// a client is told is that it is subscribed. Messages only come after.
	for i := range 300 {
		conn, err := net.DialTimeout("tcp", addr, replyTimeout)
		if err != nil {
			t.Fatalf("subscriber %d: dial: %v", i, err)
		}
		if _, err := conn.Write([]byte(encodeCommand("SUBSCRIBE", "foo"))); err != nil {
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
		second, err := readPush(r)
		if err != nil {
			t.Fatalf("subscriber %d: reading a message: %v", i, err)
		}
		if got, want := strings.Join(second, " "), "message foo flood"; got != want {
			t.Fatalf("subscriber %d: received %q after the confirmation, want %q", i, got, want)
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

func TestSlowSubscriberDoesNotHoldUpTheOthers(t *testing.T) {
	addr := startTestServer(t)
	// This subscriber never reads what it is sent.
	subscribe(t, addr, "foo")
	reader := subscribe(t, addr, "foo")
	publisher := dial(t, addr)

	// Much more than fits in the buffers of a connection is published.
	const messages = 64
	payload := strings.Repeat("0123456789abcdef", 16*1024)
	received := make(chan error, 1)
	go func() {
		want := message("foo", payload)
		got := make([]byte, len(want))
		for i := range messages {
			reader.SetReadDeadline(time.Now().Add(5 * replyTimeout))
			if _, err := io.ReadFull(reader, got); err != nil {
				received <- fmt.Errorf("reading message %d: %w", i, err)
				return
			}
			if string(got) != want {
				received <- fmt.Errorf("message %d isn't the one that was published", i)
				return
			}
		}
		received <- nil
	}()

	// The publisher isn't made to wait for the client that doesn't read, and
	// neither is the subscriber that does.
	for range messages {
		do(t, publisher, "PUBLISH", "foo", payload)
		expectReply(t, publisher, ":2\r\n")
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}

	// The server serves its other clients meanwhile.
	do(t, publisher, "PING")
	expectReply(t, publisher, "+PONG\r\n")
}

func TestRepliesAndMessagesAreNotInterleaved(t *testing.T) {
	addr := startTestServer(t)
	subscriber := subscribe(t, addr, "foo")

	const pings, messages = 300, 300
	errs := make(chan error, 2)

	// The subscriber sends commands while messages are published to it.
	go func() {
		for i := range pings {
			if _, err := subscriber.Write([]byte(encodeCommand("PING", "ping-"+strconv.Itoa(i)))); err != nil {
				errs <- fmt.Errorf("subscriber: write: %w", err)
				return
			}
		}
		errs <- nil
	}()
	go func() {
		conn, err := net.DialTimeout("tcp", addr, replyTimeout)
		if err != nil {
			errs <- fmt.Errorf("publisher: dial: %w", err)
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		for i := range messages {
			if _, err := conn.Write([]byte(encodeCommand("PUBLISH", "foo", "message-"+strconv.Itoa(i)))); err != nil {
				errs <- fmt.Errorf("publisher: write: %w", err)
				return
			}
			conn.SetReadDeadline(time.Now().Add(replyTimeout))
			if line, err := r.ReadString('\n'); err != nil || line != ":1\r\n" {
				errs <- fmt.Errorf("publisher: reply = %q, %v, want :1", line, err)
				return
			}
		}
		errs <- nil
	}()

	// Whatever way the two get mixed, the subscriber receives whole arrays:
	// the replies in the order of its commands, the messages in the order
	// they were published.
	r := bufio.NewReader(subscriber)
	nextPing, nextMessage := 0, 0
	for nextPing < pings || nextMessage < messages {
		subscriber.SetReadDeadline(time.Now().Add(replyTimeout))
		push, err := readPush(r)
		if err != nil {
			t.Fatalf("after %d replies and %d messages: %v", nextPing, nextMessage, err)
		}
		switch got := strings.Join(push, " "); got {
		case "pong ping-" + strconv.Itoa(nextPing):
			nextPing++
		case "message foo message-" + strconv.Itoa(nextMessage):
			nextMessage++
		default:
			t.Fatalf("received %q, want reply %d or message %d", got, nextPing, nextMessage)
		}
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func TestMessagesOfSeveralPublishersReachEverySubscriberInTheSameOrder(t *testing.T) {
	addr := startTestServer(t)

	const subscribers, publishers, messages = 3, 4, 100
	conns := make([]net.Conn, subscribers)
	for i := range conns {
		conns[i] = subscribe(t, addr, "foo")
	}

	errs := make(chan error, publishers)
	for id := range publishers {
		go func() {
			conn, err := net.DialTimeout("tcp", addr, replyTimeout)
			if err != nil {
				errs <- fmt.Errorf("publisher %d: dial: %w", id, err)
				return
			}
			defer conn.Close()
			r := bufio.NewReader(conn)
			for n := range messages {
				if _, err := conn.Write([]byte(encodeCommand("PUBLISH", "foo", fmt.Sprintf("%d-%d", id, n)))); err != nil {
					errs <- fmt.Errorf("publisher %d: write: %w", id, err)
					return
				}
				conn.SetReadDeadline(time.Now().Add(replyTimeout))
				if line, err := r.ReadString('\n'); err != nil || line != ":3\r\n" {
					errs <- fmt.Errorf("publisher %d: reply = %q, %v, want :3", id, line, err)
					return
				}
			}
			errs <- nil
		}()
	}

	// What each subscriber receives, in the order it receives it.
	sequences := make([][]string, subscribers)
	for i, conn := range conns {
		r := bufio.NewReader(conn)
		next := make([]int, publishers)
		for range publishers * messages {
			conn.SetReadDeadline(time.Now().Add(replyTimeout))
			push, err := readPush(r)
			if err != nil {
				t.Fatalf("subscriber %d: after %d messages: %v", i, len(sequences[i]), err)
			}
			if len(push) != 3 || push[0] != "message" || push[1] != "foo" {
				t.Fatalf("subscriber %d: received %q, want a message of foo", i, push)
			}

			// The messages of a publisher arrive in the order it sent them.
			var id, n int
			if _, err := fmt.Sscanf(push[2], "%d-%d", &id, &n); err != nil || id < 0 || id >= publishers {
				t.Fatalf("subscriber %d: unexpected message %q", i, push[2])
			}
			if n != next[id] {
				t.Fatalf("subscriber %d: received message %d of publisher %d, want its message %d", i, n, id, next[id])
			}
			next[id]++
			sequences[i] = append(sequences[i], push[2])
		}
	}
	for range publishers {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}

	// And the publishers' messages are mixed the same way for everyone.
	for i := 1; i < subscribers; i++ {
		if got, want := strings.Join(sequences[i], " "), strings.Join(sequences[0], " "); got != want {
			t.Errorf("subscriber %d didn't receive the messages in the same order as subscriber 0", i)
		}
	}
}
