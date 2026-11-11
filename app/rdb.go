package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// This file deals with RDB files, the binary format of Redis snapshots.

// emptyRDBHex is the hexadecimal dump of an RDB file that holds no key, as
// written by Redis 7.2: the "REDIS0011" header, a few metadata fields, the
// end-of-file marker and the checksum of the whole.
const emptyRDBHex = "524544495330303131fa0972656469732d76657205372e322e30fa0a72656469732d62697473c040" +
	"fa056374696d65c26d08bc65fa08757365642d6d656dc2b0c41000fa08616f662d62617365c000ff" +
	"f06e3bfec0ff5aa2"

// emptyRDB returns the contents of an RDB file that holds no key. The caller
// owns the returned slice.
func emptyRDB() []byte {
	file, err := hex.DecodeString(emptyRDBHex)
	if err != nil {
		panic("emptyRDBHex is not valid hexadecimal: " + err.Error())
	}
	return file
}

// errRDB is returned (wrapped) when an RDB file can't be parsed.
var errRDB = errors.New("invalid RDB file")

// Opcodes that introduce the sections of an RDB file. Any other byte found
// where an opcode is expected is the type of a value: a key follows.
const (
	rdbOpAux          = 0xFA // metadata: a name and a value
	rdbOpResizeDB     = 0xFB // sizes of the hash tables of the database
	rdbOpExpireTimeMs = 0xFC // expiry of the next key, in milliseconds
	rdbOpExpireTime   = 0xFD // expiry of the next key, in seconds
	rdbOpSelectDB     = 0xFE // start of a database
	rdbOpEOF          = 0xFF // end of the file, followed by its checksum
	rdbOpIdle         = 0xF8 // idle time of the next key (LRU)
	rdbOpFreq         = 0xF9 // access frequency of the next key (LFU)
)

// rdbTypeString is the value type of strings, the only one that is supported.
const rdbTypeString = 0

// rdbKey is a key read from an RDB file.
type rdbKey struct {
	key, value string
	// expiresAt is the instant the key expires, or the zero time if it
	// doesn't.
	expiresAt time.Time
}

// parseRDB reads the contents of an RDB file and returns the keys of its
// first database (number 0, the only one the server has), in the order they
// are stored. Keys are returned whether they have expired or not.
//
// The checksum at the end of the file isn't verified.
func parseRDB(file []byte) ([]rdbKey, error) {
	r := &rdbReader{data: file}

	header, err := r.bytes(9)
	if err != nil || string(header[:5]) != "REDIS" {
		return nil, fmt.Errorf("%w: missing REDIS header", errRDB)
	}
	if _, err := strconv.Atoi(string(header[5:])); err != nil {
		return nil, fmt.Errorf("%w: invalid version %q", errRDB, header[5:])
	}

	var (
		keys      []rdbKey
		db        uint64
		expiresAt time.Time
	)
	for {
		op, err := r.byte()
		if err != nil {
			return nil, fmt.Errorf("%w: no end-of-file marker", errRDB)
		}

		switch op {
		case rdbOpEOF:
			return keys, nil

		case rdbOpAux:
			if _, err := r.string(); err != nil {
				return nil, err
			}
			if _, err := r.string(); err != nil {
				return nil, err
			}

		case rdbOpSelectDB:
			if db, err = r.length(); err != nil {
				return nil, err
			}

		case rdbOpResizeDB:
			if _, err := r.length(); err != nil {
				return nil, err
			}
			if _, err := r.length(); err != nil {
				return nil, err
			}

		case rdbOpExpireTimeMs:
			b, err := r.bytes(8)
			if err != nil {
				return nil, err
			}
			expiresAt = time.UnixMilli(int64(binary.LittleEndian.Uint64(b)))

		case rdbOpExpireTime:
			b, err := r.bytes(4)
			if err != nil {
				return nil, err
			}
			expiresAt = time.Unix(int64(binary.LittleEndian.Uint32(b)), 0)

		case rdbOpIdle:
			if _, err := r.length(); err != nil {
				return nil, err
			}

		case rdbOpFreq:
			if _, err := r.byte(); err != nil {
				return nil, err
			}

		case rdbTypeString:
			key, err := r.string()
			if err != nil {
				return nil, err
			}
			value, err := r.string()
			if err != nil {
				return nil, err
			}
			if db == 0 {
				keys = append(keys, rdbKey{key: key, value: value, expiresAt: expiresAt})
			}
			// An expiry only applies to the key that follows it.
			expiresAt = time.Time{}

		default:
			return nil, fmt.Errorf("%w: unsupported value type or opcode %#x", errRDB, op)
		}
	}
}

// rdbReader reads the building blocks of the RDB format from a file held in
// memory.
type rdbReader struct {
	data []byte
	pos  int
}

// bytes returns the next n bytes of the file.
func (r *rdbReader) bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.pos {
		return nil, fmt.Errorf("%w: unexpected end of file", errRDB)
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// byte returns the next byte of the file.
func (r *rdbReader) byte() (byte, error) {
	b, err := r.bytes(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// length reads a length-encoded number, which is how sizes and counts are
// stored.
func (r *rdbReader) length() (uint64, error) {
	n, special, err := r.lengthOrEncoding()
	if err != nil {
		return 0, err
	}
	if special {
		return 0, fmt.Errorf("%w: expected a length, got a string encoding", errRDB)
	}
	return n, nil
}

// lengthOrEncoding reads a length prefix. The two most significant bits of
// its first byte tell how the number is stored:
//
//	00  in the 6 other bits of the byte
//	01  in the 6 other bits and the next byte (14 bits, big-endian)
//	10  in the next 4 bytes (big-endian), or the next 8 if the byte is 0x81
//	11  the prefix isn't a length: the 6 other bits name the special format
//	    of the string that follows, and special is set
func (r *rdbReader) lengthOrEncoding() (n uint64, special bool, err error) {
	first, err := r.byte()
	if err != nil {
		return 0, false, err
	}

	switch first >> 6 {
	case 0b00:
		return uint64(first & 0x3F), false, nil
	case 0b01:
		next, err := r.byte()
		if err != nil {
			return 0, false, err
		}
		return uint64(first&0x3F)<<8 | uint64(next), false, nil
	case 0b10:
		switch first {
		case 0x80:
			b, err := r.bytes(4)
			if err != nil {
				return 0, false, err
			}
			return uint64(binary.BigEndian.Uint32(b)), false, nil
		case 0x81:
			b, err := r.bytes(8)
			if err != nil {
				return 0, false, err
			}
			return binary.BigEndian.Uint64(b), false, nil
		default:
			return 0, false, fmt.Errorf("%w: invalid length prefix %#x", errRDB, first)
		}
	default:
		return uint64(first & 0x3F), true, nil
	}
}

// Special formats of strings, named by a length prefix that starts with the
// bits 11.
const (
	rdbEncInt8  = 0 // an 8-bit signed integer
	rdbEncInt16 = 1 // a 16-bit signed integer, little-endian
	rdbEncInt32 = 2 // a 32-bit signed integer, little-endian
	rdbEncLZF   = 3 // a string compressed with LZF
)

// string reads a string-encoded value: a length followed by that many bytes,
// or one of the special formats. Integers are returned in decimal.
func (r *rdbReader) string() (string, error) {
	n, special, err := r.lengthOrEncoding()
	if err != nil {
		return "", err
	}
	if !special {
		if n > uint64(len(r.data)) {
			return "", fmt.Errorf("%w: unexpected end of file", errRDB)
		}
		b, err := r.bytes(int(n))
		return string(b), err
	}

	switch n {
	case rdbEncInt8:
		b, err := r.bytes(1)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(int(int8(b[0]))), nil
	case rdbEncInt16:
		b, err := r.bytes(2)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(int(int16(binary.LittleEndian.Uint16(b)))), nil
	case rdbEncInt32:
		b, err := r.bytes(4)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(int(int32(binary.LittleEndian.Uint32(b)))), nil
	case rdbEncLZF:
		compressedLength, err := r.length()
		if err != nil {
			return "", err
		}
		length, err := r.length()
		if err != nil {
			return "", err
		}
		if compressedLength > uint64(len(r.data)) || length > maxBulkLength {
			return "", fmt.Errorf("%w: invalid compressed string length", errRDB)
		}
		compressed, err := r.bytes(int(compressedLength))
		if err != nil {
			return "", err
		}
		b, err := lzfDecompress(compressed, int(length))
		return string(b), err
	default:
		return "", fmt.Errorf("%w: unknown string encoding %d", errRDB, n)
	}
}

// lzfDecompress decompresses data compressed with LZF, which is known to
// expand to length bytes.
//
// Compressed data is a sequence of chunks. The 3 most significant bits of a
// chunk's first byte tell what it is: 000 starts a run of literal bytes,
// whose number minus one is in the 5 other bits. Anything else is a reference
// to bytes that were output before: the 3 bits are the number of bytes to
// copy minus two (7 meaning that the next byte adds to it), and the 5 other
// bits, with the last byte of the chunk, how far back to copy them from,
// minus one.
func lzfDecompress(data []byte, length int) ([]byte, error) {
	invalid := fmt.Errorf("%w: invalid compressed string", errRDB)

	out := make([]byte, 0, length)
	for i := 0; i < len(data); {
		ctrl := int(data[i])
		i++

		if ctrl < 32 {
			n := ctrl + 1
			if n > len(data)-i {
				return nil, invalid
			}
			out = append(out, data[i:i+n]...)
			i += n
			continue
		}

		n := ctrl >> 5
		if n == 7 {
			if i >= len(data) {
				return nil, invalid
			}
			n += int(data[i])
			i++
		}
		if i >= len(data) {
			return nil, invalid
		}
		from := len(out) - (ctrl&0x1F)<<8 - int(data[i]) - 1
		i++
		if from < 0 {
			return nil, invalid
		}
		// The bytes are copied one at a time: the reference may overlap what
		// it produces.
		for k := 0; k < n+2; k++ {
			out = append(out, out[from+k])
		}
		if len(out) > length {
			return nil, invalid
		}
	}
	if len(out) != length {
		return nil, invalid
	}
	return out, nil
}

// loadRDBFile loads the keys of the RDB file the server is configured with
// into the keyspace. Keys that have already expired are left out. A file that
// doesn't exist is the same as an empty one: there is nothing to load.
//
// The RDB file isn't used when AOF persistence is enabled: the append-only
// file is then what the dataset is rebuilt from.
func (s *Server) loadRDBFile() error {
	if s.cfg.appendOnly {
		return nil
	}
	file, err := os.ReadFile(filepath.Join(s.cfg.dir, s.cfg.dbFilename))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	keys, err := parseRDB(file)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.store.now()
	for _, k := range keys {
		if !k.expiresAt.IsZero() && !now.Before(k.expiresAt) {
			continue
		}
		s.store.setString(k.key, k.value, k.expiresAt)
	}
	return nil
}
