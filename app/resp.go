package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// maxBulkLength mirrors Redis' default proto-max-bulk-len (512MB).
	maxBulkLength = 512 * 1024 * 1024
	// maxArrayLength mirrors the largest multibulk count Redis accepts.
	maxArrayLength = 1024 * 1024
)

// errProtocol is returned (wrapped) when a client sends bytes that aren't
// valid RESP. The connection can't be resynchronised after that.
var errProtocol = errors.New("Protocol error")

// readCommand reads the next command sent by a client and returns its name
// followed by its arguments, e.g. ["ECHO", "hey"].
//
// Clients normally send a command as a RESP array of bulk strings
// ("*2\r\n$4\r\nECHO\r\n$3\r\nhey\r\n"), but a plain space separated line
// ("ECHO hey\r\n", an "inline command") is accepted as well. Empty commands
// are skipped.
//
// io.EOF is returned only when the stream ends cleanly between two commands.
func readCommand(r *bufio.Reader) ([]string, error) {
	for {
		line, err := readLine(r)
		if err != nil {
			return nil, err
		}

		var args []string
		if strings.HasPrefix(line, "*") {
			args, err = readArrayBody(r, line)
			if err != nil {
				return nil, err
			}
		} else {
			args = strings.Fields(line)
		}

		if len(args) > 0 {
			return args, nil
		}
	}
}

// readArrayBody reads the elements of a RESP array whose header line
// ("*<count>") has already been consumed.
func readArrayBody(r *bufio.Reader, header string) ([]string, error) {
	count, err := strconv.Atoi(header[1:])
	if err != nil || count > maxArrayLength {
		return nil, fmt.Errorf("%w: invalid multibulk length", errProtocol)
	}
	if count <= 0 {
		return nil, nil
	}

	args := make([]string, 0, min(count, 64))
	for i := 0; i < count; i++ {
		line, err := readLine(r)
		if err != nil {
			return nil, midCommand(err)
		}
		if !strings.HasPrefix(line, "$") {
			return nil, fmt.Errorf("%w: expected '$', got '%s'", errProtocol, firstChar(line))
		}
		length, err := strconv.Atoi(line[1:])
		if err != nil || length < 0 || length > maxBulkLength {
			return nil, fmt.Errorf("%w: invalid bulk length", errProtocol)
		}

		// The payload is followed by a CRLF that isn't part of the string.
		payload := make([]byte, length+2)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, midCommand(err)
		}
		if payload[length] != '\r' || payload[length+1] != '\n' {
			return nil, fmt.Errorf("%w: bulk string not terminated by CRLF", errProtocol)
		}
		args = append(args, string(payload[:length]))
	}
	return args, nil
}

// readLine reads up to the next line terminator and returns the line without
// it. RESP lines end in CRLF; a bare LF is tolerated for hand-typed commands.
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return "", io.ErrUnexpectedEOF
		}
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

// midCommand turns a clean EOF into io.ErrUnexpectedEOF: running out of input
// halfway through a command is never a clean end of stream.
func midCommand(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// firstChar returns the first byte of s as a string, or "" if s is empty.
func firstChar(s string) string {
	if s == "" {
		return ""
	}
	return s[:1]
}
