package main

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
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
	// aof is the append-only file the commands that modify the dataset are
	// logged to, or nil if AOF persistence is disabled.
	aof *os.File
	// loading is set while the server rebuilds its dataset by replaying the
	// append-only file, at startup.
	loading bool
	// aofSyncs counts the times the append-only file was flushed to the
	// disk.
	aofSyncs int
	// aofDirty is set when the append-only file holds data that hasn't been
	// flushed to the disk yet.
	aofDirty bool
	// aofSyncInterval is how often the append-only file is flushed with the
	// "everysec" policy.
	aofSyncInterval time.Duration
	// aofStop is closed to stop the goroutine that flushes the append-only
	// file periodically, if there is one.
	aofStop chan struct{}
	// masterOffset is, on a replica, the number of bytes of the master's
	// replication stream the replica has processed so far.
	masterOffset int64
	// writeOffset is the replication offset right after the last write that
	// was propagated: a replica that got that far has every write made so
	// far. The stream may go further, with commands that aren't writes.
	writeOffset int64
	// ackWaiters holds the clients blocked in WAIT, until enough replicas
	// have acknowledged the writes they wait for.
	ackWaiters map[*ackWaiter]struct{}
	// replicas holds the clients that are replicas of this server: the ones
	// that completed the handshake with PSYNC.
	replicas map[*client]struct{}
	// batching is set while the commands to propagate are held back in batch
	// instead of being sent to the replicas, which happens for the duration
	// of a transaction.
	batching bool
	batch    [][]string
	// watchers holds the clients watching a key with WATCH, by key. Keys
	// nobody watches have no entry.
	watchers map[string]map[*client]struct{}
	// subscribers holds the clients subscribed to a channel with SUBSCRIBE,
	// by channel. Channels nobody is subscribed to have no entry.
	subscribers map[string]map[*client]struct{}
	// users holds the users of the server, by name.
	users map[string]*user
	// defaultUser is the user clients are authenticated as when they
	// connect. It is the same one for as long as the server runs.
	defaultUser *user
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
		cfg:             cfg,
		replID:          newReplicationID(),
		aofSyncInterval: time.Second,
		store:           newStore(),
		listWaiters:     make(waitQueues[*listWaiter]),
		streamWaiters:   make(waitQueues[*streamWaiter]),
		watchers:        make(map[string]map[*client]struct{}),
		subscribers:     make(map[string]map[*client]struct{}),
		replicas:        make(map[*client]struct{}),
		ackWaiters:      make(map[*ackWaiter]struct{}),
		// The default user has no password to start with, which is why
		// clients don't have to authenticate.
		defaultUser: &user{name: defaultUserName, nopass: true},
	}
	s.store.onModify = s.keyModified
	s.users = map[string]*user{s.defaultUser.name: s.defaultUser}
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
