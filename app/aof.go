package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements AOF persistence: logging the commands that modify the
// dataset to an append-only file.

// appendDir returns the path of the directory that holds the append-only
// files.
func (cfg config) appendDir() string {
	return filepath.Join(cfg.dir, cfg.appendDirName)
}

// incrAOFName returns the name of the incremental append-only file: the one
// commands are appended to as they are executed. It is the first, and only,
// file of its kind, hence the 1 in its name.
func (cfg config) incrAOFName() string {
	return cfg.appendFilename + ".1.incr.aof"
}

// incrAOFPath returns the path of the incremental append-only file.
func (cfg config) incrAOFPath() string {
	return filepath.Join(cfg.appendDir(), cfg.incrAOFName())
}

// manifestPath returns the path of the manifest: the file that lists the
// append-only files, in the order they must be replayed to rebuild the
// dataset.
func (cfg config) manifestPath() string {
	return filepath.Join(cfg.appendDir(), cfg.appendFilename+".manifest")
}

// manifestContents returns what the manifest holds for a server that has a
// single incremental append-only file. Each file is described by a line of
// space-separated fields: its name, its sequence number among the files of
// its type and its type, "i" for incremental.
func (cfg config) manifestContents() string {
	return "file " + cfg.incrAOFName() + " seq 1 type i\n"
}

// setupAOF prepares AOF persistence, if it is enabled:
//
//   - it makes sure the directory of the append-only files exists, creating
//     it (and the data directory above it) if needed;
//   - it reads the manifest to find which file is the incremental
//     append-only file. Without a manifest, the file has its default name and
//     a manifest that lists it is written;
//   - it replays the commands that file holds, if it exists, to rebuild
//     the dataset as it was when they were logged;
//   - it opens the file, creating it empty if it doesn't exist. A file that
//     is already there is left as it is: commands are added at its end.
//
// It does nothing when AOF persistence is disabled.
func (s *Server) setupAOF() error {
	if !s.cfg.appendOnly {
		return nil
	}
	if err := os.MkdirAll(s.cfg.appendDir(), 0o755); err != nil {
		return fmt.Errorf("creating the append-only directory: %w", err)
	}

	name := s.cfg.incrAOFName()
	manifest, err := os.ReadFile(s.cfg.manifestPath())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := createFileIfMissing(s.cfg.manifestPath(), s.cfg.manifestContents()); err != nil {
			return fmt.Errorf("creating the manifest of the append-only files: %w", err)
		}
	case err != nil:
		return fmt.Errorf("reading the manifest of the append-only files: %w", err)
	default:
		listed, found, err := incrFileOfManifest(string(manifest))
		if err != nil {
			return fmt.Errorf("reading the manifest of the append-only files: %w", err)
		}
		if found {
			name = listed
		}
	}

	path := filepath.Join(s.cfg.appendDir(), name)
	if err := s.replayAOF(path); err != nil {
		return fmt.Errorf("replaying the append-only file %s: %w", name, err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("opening the append-only file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.aof = file
	if s.cfg.appendFsync == "everysec" {
		s.aofStop = make(chan struct{})
		go s.syncAOFPeriodically(file, s.aofSyncInterval, s.aofStop)
	}
	return nil
}

// syncAOFPeriodically flushes the append-only file to the disk at regular
// intervals, when something was written to it in the meantime, until stop is
// closed. It is what the "everysec" policy is about: at most one interval's
// worth of writes can be lost in a crash, and no write has to wait for the
// disk.
func (s *Server) syncAOFPeriodically(file *os.File, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}

		s.mu.Lock()
		dirty := s.aofDirty
		s.aofDirty = false
		s.mu.Unlock()
		if !dirty {
			continue
		}

		// The disk is waited for without holding the lock: the server keeps
		// serving its clients meanwhile.
		if err := file.Sync(); err != nil {
			continue // the file was closed, or the disk is failing
		}
		s.mu.Lock()
		s.aofSyncs++
		s.mu.Unlock()
	}
}

// replayAOF executes the commands logged in the append-only file at path, in
// order, as if a client had sent them: that rebuilds the dataset the server
// had when it logged them. A file that doesn't exist holds no command.
//
// A server that stops abruptly can leave a command half written at the end of
// the file. Such a command is dropped, and cut off the file so that the
// commands logged from now on are appended to a clean log. A file that is
// invalid anywhere else isn't replayed at all: it is an error.
func (s *Server) replayAOF(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	// While the commands of the file are executed they aren't recorded
	// again: they are already in the file.
	s.mu.Lock()
	s.loading = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.loading = false
		s.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The commands are run on behalf of a client of their own, which keeps
	// the state of the transactions found in the file.
	replayer := &client{server: s, ctx: ctx}
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		replayer.unwatch()
	}()

	read := &countingReader{r: file}
	r := bufio.NewReader(read)
	for {
		// Where the command that is about to be read starts in the file.
		start := read.n - int64(r.Buffered())

		args, err := readCommand(r)
		switch {
		case err == io.EOF:
			return nil
		case errors.Is(err, io.ErrUnexpectedEOF):
			fmt.Fprintf(os.Stderr, "The append-only file ends with an incomplete command: truncating it to %d bytes\n", start)
			return os.Truncate(path, start)
		case err != nil:
			return fmt.Errorf("at offset %d: %w", start, err)
		}
		replayer.execute(args)
	}
}

// incrFileOfManifest returns the name of the incremental append-only file
// listed in a manifest, and whether there is one. If the manifest lists
// several, the last one is the file in use.
//
// A manifest has one line per file, made of space-separated pairs of a field
// name and its value, such as "file appendonly.aof.1.incr.aof seq 1 type i".
// Incremental files are the ones whose type is "i".
func incrFileOfManifest(manifest string) (name string, found bool, err error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields)%2 != 0 {
			return "", false, fmt.Errorf("invalid manifest line %q", line)
		}

		var file, kind string
		for i := 0; i < len(fields); i += 2 {
			switch fields[i] {
			case "file":
				file = fields[i+1]
			case "type":
				kind = fields[i+1]
			}
		}
		if kind != "i" {
			continue
		}
		// The files of the manifest are in the append-only directory: a name
		// can't lead anywhere else.
		if file == "" || file != filepath.Base(file) || file == "." || file == ".." {
			return "", false, fmt.Errorf("invalid file name in manifest line %q", line)
		}
		name, found = file, true
	}
	return name, found, nil
}

// appendToAOF logs commands that modified the dataset, already encoded the
// way clients send them, at the end of the append-only file. It does nothing
// when AOF persistence is disabled.
//
// With the "always" policy the file is flushed to the disk before the call
// returns, and so before the client that sent the command gets its reply: a
// write that was acknowledged can't be lost. With the other policies the
// flush is left for later: to a periodic task with "everysec", to the
// operating system with "no".
func (s *Server) appendToAOF(data []byte) {
	if s.aof == nil {
		return
	}
	if _, err := s.aof.Write(data); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to write to the append-only file:", err)
		return
	}
	if s.cfg.appendFsync != "always" {
		s.aofDirty = true
		return
	}
	if err := s.aof.Sync(); err != nil {
		fmt.Fprintln(os.Stderr, "Failed to flush the append-only file:", err)
		return
	}
	s.aofSyncs++
}

// closeAOF closes the append-only file, if it is open.
func (s *Server) closeAOF() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aof == nil {
		return nil
	}
	if s.aofStop != nil {
		close(s.aofStop)
		s.aofStop = nil
	}
	err := s.aof.Close()
	s.aof = nil
	return err
}

// createFileIfMissing creates the file at path with the given contents. A
// file that already exists is left untouched.
func createFileIfMissing(path, contents string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
