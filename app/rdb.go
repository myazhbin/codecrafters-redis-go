package main

import "encoding/hex"

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
