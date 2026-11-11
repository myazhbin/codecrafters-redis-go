package main

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// connectReplicas connects n replicas to the master at addr.
func connectReplicas(t *testing.T, addr string, n int) []net.Conn {
	t.Helper()

	replicas := make([]net.Conn, n)
	for i := range replicas {
		replicas[i] = connectReplica(t, addr)
	}
	return replicas
}

func TestWritesArePropagatedToEveryReplica(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)

	for _, command := range [][]string{{"SET", "foo", "1"}, {"SET", "bar", "2"}, {"SET", "baz", "3"}} {
		conn := dial(t, addr)
		do(t, conn, command...)
		expectReply(t, conn, "+OK\r\n")
	}

	for _, replica := range replicas {
		expectPropagated(t, replica, "SET", "foo", "1")
		expectPropagated(t, replica, "SET", "bar", "2")
		expectPropagated(t, replica, "SET", "baz", "3")
		expectNoReply(t, replica, 20*time.Millisecond)
	}
}

func TestReplicaThatConnectsLaterOnlyGetsLaterWrites(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)

	early := connectReplica(t, addr)
	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	late := connectReplica(t, addr)
	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, "+OK\r\n")

	expectPropagated(t, early, "SET", "foo", "1")
	expectPropagated(t, early, "SET", "bar", "2")
	expectPropagated(t, late, "SET", "bar", "2")
	expectNoReply(t, late, 50*time.Millisecond)
}

func TestOtherReplicasKeepReceivingWhenOneDisconnects(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)
	replicas := connectReplicas(t, addr, 3)
	conn := dial(t, addr)

	do(t, conn, "SET", "foo", "1")
	expectReply(t, conn, "+OK\r\n")

	replicas[1].Close()
	eventually(t, "the master to forget the disconnected replica", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.replicas) == 2
	})

	do(t, conn, "SET", "bar", "2")
	expectReply(t, conn, "+OK\r\n")

	for _, replica := range []net.Conn{replicas[0], replicas[2]} {
		expectPropagated(t, replica, "SET", "foo", "1")
		expectPropagated(t, replica, "SET", "bar", "2")
	}
}

func TestSlowReplicaDoesNotDelayTheOthers(t *testing.T) {
	addr := startTestServer(t)
	connectReplica(t, addr) // never reads the stream
	fast := connectReplica(t, addr)
	conn := dial(t, addr)

	// Enough data to fill the connection of the replica that doesn't read.
	value := string(make([]byte, 64*1024))
	for i := range 100 {
		do(t, conn, "SET", "key"+strconv.Itoa(i), value)
		expectReply(t, conn, "+OK\r\n")
		expectPropagated(t, fast, "SET", "key"+strconv.Itoa(i), value)
	}
}

// TestReplicasReceiveTheSameStream has many clients write at the same time:
// whatever order the master executes the commands in, every replica must be
// told the same thing.
func TestReplicasReceiveTheSameStream(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 3)

	const writers, writes = 5, 20
	var wg sync.WaitGroup
	for w := range writers {
		conn := dial(t, addr)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range writes {
				do(t, conn, "SET", "key", strconv.Itoa(w*1000+i))
			}
			// Wait for all the replies: one +OK per command.
			buf := make([]byte, len("+OK\r\n")*writes)
			conn.SetReadDeadline(time.Now().Add(replyTimeout))
			for n := 0; n < len(buf); {
				m, err := conn.Read(buf[n:])
				if err != nil {
					t.Errorf("reading replies: %v", err)
					return
				}
				n += m
			}
		}()
	}
	wg.Wait()

	streams := make([][]string, len(replicas))
	for i, replica := range replicas {
		for range writers * writes {
			// Values vary in length, so the command is read piece by piece.
			if line := readReplyLine(t, replica); line != "*3" {
				t.Fatalf("replica %d: got %q, want an array of 3 elements", i, line)
			}
			name, key, value := readBulkString(t, replica), readBulkString(t, replica), readBulkString(t, replica)
			if name != "SET" || key != "key" || value == "" {
				t.Fatalf("replica %d: unexpected command %s %s %s", i, name, key, value)
			}
			streams[i] = append(streams[i], value)
		}
		expectNoReply(t, replica, 20*time.Millisecond)
	}

	for i := 1; i < len(streams); i++ {
		for j := range streams[0] {
			if streams[i][j] != streams[0][j] {
				t.Fatalf("replica %d received value %q at position %d, replica 0 received %q",
					i, streams[i][j], j, streams[0][j])
			}
		}
	}

	// And the stream ends with the value the master ended up with.
	conn := dial(t, addr)
	do(t, conn, "GET", "key")
	if got, want := readBulkString(t, conn), streams[0][len(streams[0])-1]; got != want {
		t.Errorf("master holds %q, the last propagated value is %q", got, want)
	}
}

func TestTransactionIsPropagatedToEveryReplica(t *testing.T) {
	addr := startTestServer(t)
	replicas := connectReplicas(t, addr, 2)
	conn := dial(t, addr)

	do(t, conn, "MULTI")
	do(t, conn, "SET", "foo", "1")
	do(t, conn, "INCR", "foo")
	do(t, conn, "EXEC")
	drainPendingReply(t, conn)

	for _, replica := range replicas {
		expectPropagated(t, replica, "MULTI")
		expectPropagated(t, replica, "SET", "foo", "1")
		expectPropagated(t, replica, "INCR", "foo")
		expectPropagated(t, replica, "EXEC")
	}
}
