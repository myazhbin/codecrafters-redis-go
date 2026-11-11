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

// setupAOF prepares AOF persistence, if it is enabled: it makes sure the
// directory of the append-only files exists, creating it (and the data
// directory above it) if needed. It does nothing when AOF persistence is
// disabled.
func (s *Server) setupAOF() error {
	if !s.cfg.appendOnly {
		return nil
	}
	if err := os.MkdirAll(s.cfg.appendDir(), 0o755); err != nil {
		return fmt.Errorf("creating the append-only directory: %w", err)
	}
	return nil
}
