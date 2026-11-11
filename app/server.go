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
	// listWaiters holds the clients blocked until an element is pushed to a
	// list, by list key.
	listWaiters waitQueues[*listWaiter]
	// streamWaiters holds the clients blocked until an entry is added to a
	// stream, by stream key.
	streamWaiters waitQueues[*streamWaiter]
	// watchers holds the clients watching a key with WATCH, by key. Keys
	// nobody watches have no entry.
	watchers map[string]map[*client]struct{}
}

// NewServer returns a server with an empty keyspace that is ready to Serve.
func NewServer() *Server {
	s := &Server{
		store:         newStore(),
		listWaiters:   make(waitQueues[*listWaiter]),
		streamWaiters: make(waitQueues[*streamWaiter]),
		watchers:      make(map[string]map[*client]struct{}),
	}
	s.store.onModify = s.keyModified
	return s
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
