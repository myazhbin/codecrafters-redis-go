package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// This file implements AOF persistence: logging the commands that modify the
// dataset to an append-only file.

// appendDir returns the path of the directory that holds the append-only
// files.
func (cfg config) appendDir() string {
	return filepath.Join(cfg.dir, cfg.appendDirName)
}

// incrAOFPath returns the path of the incremental append-only file: the one
// commands are appended to as they are executed. It is the first, and only,
// file of its kind, hence the 1 in its name.
func (cfg config) incrAOFPath() string {
	return filepath.Join(cfg.appendDir(), cfg.appendFilename+".1.incr.aof")
}

// setupAOF prepares AOF persistence, if it is enabled:
//
//   - it makes sure the directory of the append-only files exists, creating
//     it (and the data directory above it) if needed;
//   - it opens the incremental append-only file, creating it empty if it
//     doesn't exist. A file that is already there is left as it is.
//
// It does nothing when AOF persistence is disabled.
func (s *Server) setupAOF() error {
	if !s.cfg.appendOnly {
		return nil
	}
	if err := os.MkdirAll(s.cfg.appendDir(), 0o755); err != nil {
		return fmt.Errorf("creating the append-only directory: %w", err)
	}

	file, err := os.OpenFile(s.cfg.incrAOFPath(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("opening the append-only file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.aof = file
	return nil
}

// closeAOF closes the append-only file, if it is open.
func (s *Server) closeAOF() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aof == nil {
		return nil
	}
	err := s.aof.Close()
	s.aof = nil
	return err
}
