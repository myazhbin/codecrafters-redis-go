package main

import (
	"errors"
	"fmt"
	"io/fs"
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
//   - it opens the incremental append-only file, creating it empty if it
//     doesn't exist. A file that is already there is left as it is;
//   - it writes the manifest that lists that file, unless there is one
//     already.
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

	if err := createFileIfMissing(s.cfg.manifestPath(), s.cfg.manifestContents()); err != nil {
		file.Close()
		return fmt.Errorf("creating the manifest of the append-only files: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.aof = file
	return nil
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
