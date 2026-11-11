package main

import (
	"cmp"
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

// compare returns -1 if id comes before other, +1 if it comes after, and 0 if
// they are the same ID. IDs are ordered by their milliseconds part first, then
// by their sequence number.
func (id streamID) compare(other streamID) int {
	if c := cmp.Compare(id.ms, other.ms); c != 0 {
		return c
	}
	return cmp.Compare(id.seq, other.seq)
}

// The errors below are sent to clients as they are.
var (
	// errInvalidStreamID is returned for strings that aren't an entry ID.
	errInvalidStreamID = errors.New("ERR Invalid stream ID specified as stream command argument")
	// errStreamIDIsZero is returned when adding an entry with the ID 0-0,
	// which is smaller than every ID a stream accepts.
	errStreamIDIsZero = errors.New("ERR The ID specified in XADD must be greater than 0-0")
	// errStreamIDTooSmall is returned when adding an entry whose ID doesn't
	// come after the ID of the last entry of the stream.
	errStreamIDTooSmall = errors.New("ERR The ID specified in XADD is equal or smaller than the target stream top item")
)

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

// stream is the value of a stream key: a sequence of entries ordered by ID.
// Entries can only be added at the end, with an ID greater than all the
// previous ones.
type stream struct {
	entries []streamEntry
	// lastID is the ID of the entry added last, or 0-0 if there is none yet.
	lastID streamID
}

// add appends an entry to the stream. It fails with errStreamIDTooSmall, and
// adds nothing, if id is not strictly greater than the ID of the last entry.
func (st *stream) add(id streamID, fields []string) error {
	if id.compare(st.lastID) <= 0 {
		return errStreamIDTooSmall
	}
	st.entries = append(st.entries, streamEntry{id: id, fields: fields})
	st.lastID = id
	return nil
}

// cmdXAdd implements XADD key id field value [field value ...]: it appends
// an entry to the stream stored at key, creating the stream if needed, and
// replies with the ID of the new entry.
//
// The ID must be greater than the ID of every entry already in the stream,
// and greater than 0-0.
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
	if id == (streamID{}) {
		return errorReply(errStreamIDIsZero.Error())
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

	if err := st.add(id, fields); err != nil {
		return errorReply(err.Error())
	}
	if created {
		store.setStream(key, st)
	}
	return bulkString(id.String())
}
