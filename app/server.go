package main

import (
	"errors"
	"net"
	"sync"
)

// isReplica reports whether the server replicates a master, as opposed to
// being a master itself.
func (s *Server) isReplica() bool {
	return s.cfg.replicaOf != nil
}

// Server is a Redis-compatible server that can talk to many clients at once.
type Server struct {
	// mu guards the fields below. Every command holds it for its whole
	// execution, so commands run one at a time even though each client is
	// served by its own goroutine.
	mu    sync.Mutex
	store *store
	// cfg is the configuration the server was started with.
	cfg config
	// replID is the server's replication ID: a random string that identifies
	// the history of its dataset. A server gets a new one every time it
	// starts.
	replID string
	// replOffset is the replication offset: the number of bytes of commands
	// the server has sent down the replication stream.
	replOffset int64
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

// NewServer returns a server with an empty keyspace and the default
// configuration that is ready to Serve.
func NewServer() *Server {
	return NewServerWithConfig(defaultConfig())
}

// NewServerWithConfig returns a server with an empty keyspace, configured by
// cfg, that is ready to Serve.
func NewServerWithConfig(cfg config) *Server {
	s := &Server{
		cfg:           cfg,
		replID:        newReplicationID(),
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
