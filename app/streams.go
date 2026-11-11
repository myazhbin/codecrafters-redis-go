package main

import (
	"errors"
	"strconv"
	"strings"
)

// This file implements streams and the commands that operate on them.

// streamID identifies an entry of a stream. IDs are written as
// "<milliseconds>-<sequence>": the first part is usually the time the entry
// was added at, and the second one tells apart entries added during the same
// millisecond.
type streamID struct {
	ms  uint64
	seq uint64
}

func (id streamID) String() string {
	return strconv.FormatUint(id.ms, 10) + "-" + strconv.FormatUint(id.seq, 10)
}

// errInvalidStreamID is returned for strings that aren't a stream entry ID.
var errInvalidStreamID = errors.New("ERR Invalid stream ID specified as stream command argument")

// parseStreamID parses an explicit entry ID: "<milliseconds>-<sequence>", or
// just "<milliseconds>" in which case the sequence number is 0.
func parseStreamID(s string) (streamID, error) {
	msPart, seqPart, hasSeq := strings.Cut(s, "-")

	ms, err := strconv.ParseUint(msPart, 10, 64)
	if err != nil {
		return streamID{}, errInvalidStreamID
	}
	var seq uint64
	if hasSeq {
		seq, err = strconv.ParseUint(seqPart, 10, 64)
		if err != nil {
			return streamID{}, errInvalidStreamID
		}
	}
	return streamID{ms: ms, seq: seq}, nil
}

// streamEntry is an entry of a stream: an ID and a set of field-value pairs.
type streamEntry struct {
	id streamID
	// fields holds the entry's fields and their values, alternating, in the
	// order they were given: field1, value1, field2, value2, ...
	fields []string
}

// stream is the value of a stream key: a sequence of entries in the order
// they were added.
type stream struct {
	entries []streamEntry
}

// add appends an entry to the stream.
func (st *stream) add(id streamID, fields []string) {
	st.entries = append(st.entries, streamEntry{id: id, fields: fields})
}

// cmdXAdd implements XADD key id field value [field value ...]: it appends
// an entry to the stream stored at key, creating the stream if needed, and
// replies with the ID of the new entry.
func cmdXAdd(c *client, args []string) []byte {
	store := c.server.store
	key, rawID, fields := args[0], args[1], args[2:]

	id, err := parseStreamID(rawID)
	if err != nil {
		return errorReply(err.Error())
	}
	if len(fields)%2 != 0 {
		return wrongArgumentCountError("XADD")
	}

	st, err := store.getStream(key)
	if err != nil {
		return errorReply(err.Error())
	}
	// A new stream is only stored once its first entry is in, so a rejected
	// XADD never leaves an empty stream behind.
	created := st == nil
	if created {
		st = &stream{}
	}

	st.add(id, fields)
	if created {
		store.setStream(key, st)
	}
	return bulkString(id.String())
}
