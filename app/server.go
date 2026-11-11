package main

import (
	"errors"
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
	// listWaiters holds, for each list key, the clients that are blocked
	// waiting for an element to be pushed to it. The client that has been
	// waiting the longest comes first.
	listWaiters map[string][]*listWaiter
}

// NewServer returns a server with an empty keyspace that is ready to Serve.
func NewServer() *Server {
	return &Server{
		store:       newStore(),
		listWaiters: make(map[string][]*listWaiter),
	}
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
