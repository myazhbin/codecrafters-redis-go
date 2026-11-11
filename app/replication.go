package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// This file implements replication: how a master keeps its replicas up to
// date, and how a replica follows its master.

// replicationIDLength is the number of characters of a replication ID.
const replicationIDLength = 40

// newReplicationID returns a new pseudo-random replication ID, made of 40
// hexadecimal digits.
func newReplicationID() string {
	b := make([]byte, replicationIDLength/2)
	// rand.Read never fails: it panics if the system can't provide
	// randomness.
	rand.Read(b)
	return hex.EncodeToString(b)
}

// masterDialTimeout is how long a replica waits for the connection to its
// master to be established.
const masterDialTimeout = 5 * time.Second

// followMaster makes a replica connect to its master, introduce itself with
// the replication handshake, and then stay connected. It only returns once
// the connection is over, with the error that ended it if there is one.
func (s *Server) followMaster() error {
	master := s.cfg.replicaOf.String()

	conn, err := net.DialTimeout("tcp", master, masterDialTimeout)
	if err != nil {
		return fmt.Errorf("connecting to the master at %s: %w", master, err)
	}
	defer conn.Close()

	r := bufio.NewReader(conn)
	if err := s.handshake(conn, r); err != nil {
		return fmt.Errorf("handshake with the master at %s: %w", master, err)
	}

	// Nothing is done with what the master sends yet: the connection is kept
	// open until the master closes it.
	if _, err := io.Copy(io.Discard, r); err != nil {
		return fmt.Errorf("reading from the master at %s: %w", master, err)
	}
	return nil
}

// handshake is how a replica introduces itself to its master, over the
// connection it just opened: it sends commands one at a time, waiting for the
// master to answer each of them before sending the next.
//
//   - PING checks that the master is there and answers.
//   - REPLCONF listening-port tells the master which port the replica serves
//     its own clients on.
//   - REPLCONF capa psync2 tells the master the replica understands the
//     second version of the synchronization protocol.
func (s *Server) handshake(w io.Writer, r *bufio.Reader) error {
	steps := []struct {
		command []string
		// reply is the status the master is expected to answer with.
		reply string
	}{
		{[]string{"PING"}, "PONG"},
		{[]string{"REPLCONF", "listening-port", strconv.Itoa(s.cfg.port)}, "OK"},
		{[]string{"REPLCONF", "capa", "psync2"}, "OK"},
	}

	for _, step := range steps {
		reply, err := roundTrip(w, r, step.command...)
		if err != nil {
			return fmt.Errorf("%s: %w", strings.Join(step.command, " "), err)
		}
		if reply != "+"+step.reply {
			return fmt.Errorf("%s: unexpected reply %q from the master, want %q",
				strings.Join(step.command, " "), reply, "+"+step.reply)
		}
	}
	return nil
}

// roundTrip sends a command and waits for its reply, which must fit on a
// single line: a simple string, an error or an integer. It returns that line,
// type prefix included and line terminator excluded.
func roundTrip(w io.Writer, r *bufio.Reader, command ...string) (string, error) {
	if _, err := w.Write(encodeCommandArray(command...)); err != nil {
		return "", err
	}
	reply, err := readLine(r)
	if err != nil {
		return "", fmt.Errorf("waiting for the reply: %w", midCommand(err))
	}
	return reply, nil
}

// encodeCommandArray encodes a command the way clients send it: as a RESP
// array of bulk strings holding the command's name and its arguments.
func encodeCommandArray(args ...string) []byte {
	return bulkStringArray(args)
}
