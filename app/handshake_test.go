package main

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeMaster is a listener that plays the role of a master in tests: it
// accepts the connection of the replica under test and lets the test script
// the conversation.
type fakeMaster struct {
	t        *testing.T
	listener net.Listener
}

// startFakeMaster listens on a random local port for the duration of the
// test.
func startFakeMaster(t *testing.T) *fakeMaster {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return &fakeMaster{t: t, listener: l}
}

// address returns where the fake master listens, as a replica's
// configuration expects it.
func (m *fakeMaster) address() *masterAddress {
	m.t.Helper()

	host, rawPort, err := net.SplitHostPort(m.listener.Addr().String())
	if err != nil {
		m.t.Fatalf("splitting the listener's address: %v", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		m.t.Fatalf("parsing the listener's port: %v", err)
	}
	return &masterAddress{host: host, port: port}
}

// accept waits for the replica to connect and returns the master's side of
// the connection.
func (m *fakeMaster) accept() *masterSide {
	m.t.Helper()

	m.listener.(*net.TCPListener).SetDeadline(time.Now().Add(replyTimeout))
	conn, err := m.listener.Accept()
	if err != nil {
		m.t.Fatalf("waiting for the replica to connect: %v", err)
	}
	m.t.Cleanup(func() { conn.Close() })
	return &masterSide{t: m.t, conn: conn, r: bufio.NewReader(conn)}
}

// masterSide is the fake master's end of the connection with a replica.
type masterSide struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

// expectCommand reads the next command sent by the replica, which must be
// encoded as a RESP array of bulk strings, and checks it is the one expected.
// The command's name is compared without regard to case.
func (m *masterSide) expectCommand(want ...string) {
	m.t.Helper()

	got := m.readCommand()
	if len(got) != len(want) || !strings.EqualFold(got[0], want[0]) {
		m.t.Fatalf("replica sent %q, want %q", got, want)
	}
	for i := 1; i < len(want); i++ {
		if got[i] != want[i] {
			m.t.Fatalf("replica sent %q, want %q", got, want)
		}
	}
}

// readCommand reads the next command sent by the replica, which must be
// encoded as a RESP array of bulk strings.
func (m *masterSide) readCommand() []string {
	m.t.Helper()

	m.conn.SetReadDeadline(time.Now().Add(replyTimeout))
	first, err := m.r.Peek(1)
	if err != nil {
		m.t.Fatalf("waiting for a command from the replica: %v", err)
	}
	if first[0] != '*' {
		m.t.Fatalf("replica's command starts with %q, want a RESP array", first)
	}
	args, err := readCommand(m.r)
	if err != nil {
		m.t.Fatalf("reading a command from the replica: %v", err)
	}
	return args
}

// expectSilence checks that the replica sends nothing for the duration d.
func (m *masterSide) expectSilence(d time.Duration) {
	m.t.Helper()

	m.conn.SetReadDeadline(time.Now().Add(d))
	if b, err := m.r.Peek(1); err == nil {
		m.t.Fatalf("replica sent something unexpected, starting with %q", b)
	}
}

// reply sends raw bytes to the replica.
func (m *masterSide) reply(data string) {
	m.t.Helper()

	if _, err := m.conn.Write([]byte(data)); err != nil {
		m.t.Fatalf("writing to the replica: %v", err)
	}
}

// startReplicaOf runs a replica of the fake master: it serves clients on a
// random local port and follows the master, like the program does. It
// returns the server and the address its clients dial.
func startReplicaOf(t *testing.T, master *fakeMaster) (*Server, string) {
	t.Helper()

	cfg := defaultConfig()
	cfg.replicaOf = master.address()
	srv := NewServerWithConfig(cfg)
	addr := startServer(t, srv)

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.followMaster()
	}()
	// The replica stops following when the master goes away, which the fake
	// master's own cleanup takes care of. Registered first, this cleanup runs
	// last and waits for that to happen.
	t.Cleanup(func() {
		master.listener.Close()
		select {
		case <-done:
		case <-time.After(replyTimeout):
			t.Errorf("the replica is still following its master at the end of the test")
		}
	})
	return srv, addr
}

func TestReplicaConnectsToItsMasterAndSendsPing(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.accept()
	link.expectCommand("PING")
}

func TestReplicaSendsPingAsARESPArray(t *testing.T) {
	master := startFakeMaster(t)
	startReplicaOf(t, master)

	link := master.accept()
	want := "*1\r\n$4\r\nPING\r\n"
	got := make([]byte, len(want))
	link.conn.SetReadDeadline(time.Now().Add(replyTimeout))
	if _, err := link.r.Read(got); err != nil {
		t.Fatalf("reading from the replica: %v", err)
	}
	if string(got) != want {
		t.Errorf("replica sent %q, want %q", got, want)
	}
}

func TestReplicaServesClientsWhileFollowingItsMaster(t *testing.T) {
	master := startFakeMaster(t)
	_, addr := startReplicaOf(t, master)

	link := master.accept()
	link.expectCommand("PING")

	// The master hasn't answered, and the replica's clients aren't kept
	// waiting for it.
	conn := dial(t, addr)
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	fields, _ := info(t, conn, "replication")
	if got := fields["role"]; got != "slave" {
		t.Errorf("role = %q, want slave", got)
	}
}

func TestFollowMasterFailsWhenTheMasterIsUnreachable(t *testing.T) {
	// A port nobody listens on: the listener is closed right away.
	master := startFakeMaster(t)
	address := master.address()
	master.listener.Close()

	cfg := defaultConfig()
	cfg.replicaOf = address
	err := NewServerWithConfig(cfg).followMaster()
	if err == nil {
		t.Fatalf("followMaster succeeded although the master is unreachable")
	}
	if !strings.Contains(err.Error(), "connecting to the master") {
		t.Errorf("error = %q, want it to say the connection failed", err)
	}
}

func TestFollowMasterReturnsWhenTheMasterGoesAway(t *testing.T) {
	master := startFakeMaster(t)
	cfg := defaultConfig()
	cfg.replicaOf = master.address()
	srv := NewServerWithConfig(cfg)

	done := make(chan error, 1)
	go func() { done <- srv.followMaster() }()

	link := master.accept()
	link.expectCommand("PING")
	link.conn.Close()

	select {
	case <-done:
	case <-time.After(replyTimeout):
		t.Fatalf("followMaster didn't return after the master closed the connection")
	}
}

func TestEncodeCommandArray(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"PING"}, "*1\r\n$4\r\nPING\r\n"},
		{[]string{"ECHO", "hey"}, "*2\r\n$4\r\nECHO\r\n$3\r\nhey\r\n"},
		{[]string{"SET", "k", ""}, "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n"},
	}
	for _, tt := range tests {
		if got := string(encodeCommandArray(tt.args...)); got != tt.want {
			t.Errorf("encodeCommandArray(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}
