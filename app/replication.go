package main

import (
	"crypto/rand"
	"encoding/hex"
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
