package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"sync"
)

// Server is a Redis-compatible server that can talk to many clients at once.
type Server struct {
	// mu guards the fields below. Every command holds it for its whole
	// execution, so commands run one at a time even though each client is
	// served by its own goroutine.
	mu    sync.Mutex
	store *store
}

// NewServer returns a server with an empty keyspace that is ready to Serve.
func NewServer() *Server {
	return &Server{store: newStore()}
}

// Serve accepts connections on l and serves each client on its own goroutine,
// so a slow or idle client never blocks the others. It returns nil once l is
// closed, or the error that made accepting fail.
func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConnection(conn)
	}
}

// handleConnection reads commands from the client one after another until it
// disconnects, executing each of them and sending back its reply.
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		args, err := readCommand(reader)
		if err != nil {
			if errors.Is(err, errProtocol) {
				fmt.Fprintf(conn, "-ERR %s\r\n", err)
			}
			return
		}
		if _, err := conn.Write(s.execute(args)); err != nil {
			return
		}
	}
}
