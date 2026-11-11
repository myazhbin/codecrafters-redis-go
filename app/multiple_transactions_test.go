package main

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentTransactionsHaveTheirOwnQueue(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "SET", "foo", "41")
	expectReply(t, first, queued)
	do(t, first, "INCR", "foo")
	expectReply(t, first, queued)

	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")
	do(t, second, "INCR", "foo")
	expectReply(t, second, queued)

	// Each EXEC only runs, and replies to, the commands of its own
	// transaction.
	do(t, first, "EXEC")
	expectReply(t, first, array("+OK\r\n", ":42\r\n"))
	do(t, second, "EXEC")
	expectReply(t, second, array(":43\r\n"))
}

func TestConcurrentTransactionsRunInExecOrder(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "SET", "foo", "41")
	expectReply(t, first, queued)
	do(t, first, "INCR", "foo")
	expectReply(t, first, queued)

	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")
	do(t, second, "INCR", "foo")
	expectReply(t, second, queued)

	// What matters is the order of the EXECs, not the order in which the
	// transactions were started.
	do(t, second, "EXEC")
	expectReply(t, second, array(":1\r\n"))
	do(t, first, "EXEC")
	expectReply(t, first, array("+OK\r\n", ":42\r\n"))
}

func TestManyClientsEachRunATransaction(t *testing.T) {
	addr := startTestServer(t)

	const clients = 10
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	// Every transaction is open at the same time...
	for _, conn := range conns {
		do(t, conn, "MULTI")
		expectReply(t, conn, "+OK\r\n")
		do(t, conn, "INCR", "foo")
		expectReply(t, conn, queued)
	}
	// ...and nothing has been executed yet.
	observer := dial(t, addr)
	do(t, observer, "GET", "foo")
	expectReply(t, observer, "$-1\r\n")

	for i, conn := range conns {
		do(t, conn, "EXEC")
		expectReply(t, conn, array(":"+strconv.Itoa(i+1)+"\r\n"))
	}
}

func TestInterleavedQueueingAcrossTransactions(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")

	do(t, first, "SET", "a", "1")
	expectReply(t, first, queued)
	do(t, second, "SET", "b", "2")
	expectReply(t, second, queued)
	do(t, first, "GET", "b")
	expectReply(t, first, queued)
	do(t, second, "GET", "a")
	expectReply(t, second, queued)

	do(t, first, "EXEC")
	expectReply(t, first, array("+OK\r\n", "$-1\r\n"))
	do(t, second, "EXEC")
	expectReply(t, second, array("+OK\r\n", "$1\r\n1\r\n"))
}

func TestDiscardingOneTransactionLeavesTheOthersRunning(t *testing.T) {
	addr := startTestServer(t)
	first := dial(t, addr)
	second := dial(t, addr)

	do(t, first, "MULTI")
	expectReply(t, first, "+OK\r\n")
	do(t, first, "INCR", "foo")
	expectReply(t, first, queued)
	do(t, second, "MULTI")
	expectReply(t, second, "+OK\r\n")
	do(t, second, "INCR", "foo")
	expectReply(t, second, queued)

	do(t, first, "DISCARD")
	expectReply(t, first, "+OK\r\n")
	do(t, second, "EXEC")
	expectReply(t, second, array(":1\r\n"))
}

func TestTransactionAndPlainClientAtTheSameTime(t *testing.T) {
	addr := startTestServer(t)
	conn := dial(t, addr)
	plain := dial(t, addr)

	do(t, conn, "MULTI")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "INCR", "foo")
	expectReply(t, conn, queued)

	// A client outside of any transaction is not affected by the open one.
	do(t, plain, "INCR", "foo")
	expectReply(t, plain, ":1\r\n")
	do(t, plain, "EXEC")
	expectReply(t, plain, "-ERR EXEC without MULTI\r\n")

	do(t, conn, "EXEC")
	expectReply(t, conn, array(":2\r\n"))
}

// TestParallelTransactionsLoseNoUpdate runs transactions from many clients at
// once: as each of them is atomic, none of their increments may be lost.
func TestParallelTransactionsLoseNoUpdate(t *testing.T) {
	addr := startTestServer(t)

	const clients, rounds = 8, 25
	conns := make([]net.Conn, clients)
	for i := range conns {
		conns[i] = dial(t, addr)
	}

	var wg sync.WaitGroup
	for _, conn := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := bufio.NewReader(conn)
			for range rounds {
				send(t, conn, encodeCommand("MULTI")+encodeCommand("INCR", "counter")+
					encodeCommand("INCR", "counter")+encodeCommand("EXEC"))
				if !readTransactionOfTwoIncrements(t, conn, r) {
					return
				}
			}
		}()
	}
	wg.Wait()

	observer := dial(t, addr)
	do(t, observer, "GET", "counter")
	want := strconv.Itoa(clients * rounds * 2)
	expectReply(t, observer, "$"+strconv.Itoa(len(want))+"\r\n"+want+"\r\n")
}

// readTransactionOfTwoIncrements reads the replies to MULTI, two INCRs of the
// same key and EXEC, and checks that the increments ran back to back. It is
// meant to be called from goroutines other than the test's, so it reports
// failures with Errorf and returns whether everything was as expected.
func readTransactionOfTwoIncrements(t *testing.T, conn net.Conn, r *bufio.Reader) bool {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	lines := make([]string, 6)
	for i := range lines {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Errorf("reading reply line %d: %v", i, err)
			return false
		}
		lines[i] = strings.TrimSuffix(line, "\r\n")
	}

	for i, want := range []string{"+OK", "+QUEUED", "+QUEUED", "*2"} {
		if lines[i] != want {
			t.Errorf("reply line = %q, want %q", lines[i], want)
			return false
		}
	}
	a, errA := strconv.Atoi(strings.TrimPrefix(lines[4], ":"))
	b, errB := strconv.Atoi(strings.TrimPrefix(lines[5], ":"))
	if errA != nil || errB != nil {
		t.Errorf("EXEC replied with %q and %q, want two integers", lines[4], lines[5])
		return false
	}
	if b != a+1 {
		t.Errorf("increments of a transaction were interleaved with another client's: got %d then %d", a, b)
		return false
	}
	return true
}
