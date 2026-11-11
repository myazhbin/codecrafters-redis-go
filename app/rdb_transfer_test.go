package main

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// receiveRDBTransfer reads an RDB file sent the way a master sends it,
// "$<length>\r\n<contents>", and returns the contents.
func receiveRDBTransfer(t *testing.T, conn net.Conn) []byte {
	t.Helper()

	header := readReplyLine(t, conn)
	if !strings.HasPrefix(header, "$") {
		t.Fatalf("RDB transfer starts with %q, want $<length>", header)
	}
	length, err := strconv.Atoi(header[1:])
	if err != nil || length < 0 {
		t.Fatalf("invalid RDB length in %q", header)
	}

	conn.SetReadDeadline(time.Now().Add(replyTimeout))
	file := make([]byte, length)
	if _, err := io.ReadFull(conn, file); err != nil {
		t.Fatalf("reading an RDB file of %d bytes: %v", length, err)
	}
	return file
}

func TestMasterSendsAnRDBFileAfterFullResync(t *testing.T) {
	srv := NewServer()
	conn := dial(t, startServer(t, srv))

	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
	do(t, conn, "REPLCONF", "listening-port", "6380")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "REPLCONF", "capa", "eof", "capa", "psync2")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "PSYNC", "?", "-1")
	expectReply(t, conn, "+FULLRESYNC "+srv.replID+" 0\r\n")

	file := receiveRDBTransfer(t, conn)
	if !bytes.Equal(file, emptyRDB()) {
		t.Errorf("RDB file = %x, want the empty RDB file", file)
	}
}

func TestRDBTransferHasNoTrailingCRLF(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "PSYNC", "?", "-1")
	readReplyLine(t, conn)
	receiveRDBTransfer(t, conn)

	// Unlike a bulk string, the file isn't followed by CRLF: the next thing
	// on the connection is the reply to the next command.
	do(t, conn, "PING")
	expectReply(t, conn, "+PONG\r\n")
}

func TestRDBTransferEncoding(t *testing.T) {
	got := rdbTransfer([]byte("REDIS"))
	if want := "$5\r\nREDIS"; string(got) != want {
		t.Errorf("rdbTransfer = %q, want %q", got, want)
	}
	if got := rdbTransfer(nil); string(got) != "$0\r\n" {
		t.Errorf("rdbTransfer of an empty file = %q, want $0", got)
	}

	file := emptyRDB()
	got = rdbTransfer(file)
	want := append([]byte("$"+strconv.Itoa(len(file))+"\r\n"), file...)
	if !bytes.Equal(got, want) {
		t.Errorf("rdbTransfer = %q, want %q", got, want)
	}
}

func TestEmptyRDBIsAValidRDBFile(t *testing.T) {
	file := emptyRDB()

	if len(file) != 88 {
		t.Errorf("empty RDB file is %d bytes long, want 88", len(file))
	}
	// Magic string and version.
	if !bytes.HasPrefix(file, []byte("REDIS0011")) {
		t.Errorf("empty RDB file starts with %q, want REDIS0011", file[:9])
	}
	// End-of-file marker, followed by an 8-byte checksum.
	if file[len(file)-9] != 0xFF {
		t.Errorf("byte before the checksum = %#x, want the end-of-file marker 0xFF", file[len(file)-9])
	}
	// No database section: an empty file holds no key.
	if bytes.IndexByte(file[:len(file)-9], 0xFE) != -1 {
		t.Errorf("empty RDB file has a database section")
	}
}

func TestEmptyRDBReturnsAFreshCopy(t *testing.T) {
	a := emptyRDB()
	a[0] = 'X'
	if b := emptyRDB(); b[0] != 'R' {
		t.Errorf("modifying the result of emptyRDB changed the next one")
	}
}

func TestEveryReplicaGetsTheRDBFile(t *testing.T) {
	srv := NewServer()
	addr := startServer(t, srv)

	for range 3 {
		replica := dial(t, addr)
		handshakeUntilPsync(t, replica)
		do(t, replica, "PSYNC", "?", "-1")
		expectReply(t, replica, "+FULLRESYNC "+srv.replID+" 0\r\n")
		if file := receiveRDBTransfer(t, replica); !bytes.Equal(file, emptyRDB()) {
			t.Errorf("RDB file = %x, want the empty RDB file", file)
		}
	}
}
