package main

import (
	"crypto/rand"
	"strconv"
	"testing"
)

func TestLLen(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "a", "b", "c", "d")
	expectReply(t, conn, ":4\r\n")

	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":4\r\n")
}

func TestLLenMissingList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	// A list that doesn't exist has no elements.
	do(t, conn, "LLEN", "missing_list_key")
	expectReply(t, conn, ":0\r\n")

	// Asking for the length doesn't create the list.
	do(t, conn, "LRANGE", "missing_list_key", "0", "-1")
	expectReply(t, conn, emptyArray)
	do(t, conn, "RPUSH", "missing_list_key", "first")
	expectReply(t, conn, ":1\r\n")
}

func TestLLenRandomNumberOfElements(t *testing.T) {
	conn := dial(t, startTestServer(t))

	for round := 0; round < 5; round++ {
		var random [1]byte
		if _, err := rand.Read(random[:]); err != nil {
			t.Fatalf("generating a random length: %v", err)
		}
		count := 1 + int(random[0])%40

		key := "list-" + strconv.Itoa(round)
		elements := make([]string, count)
		for i := range elements {
			elements[i] = randomWord(t)
		}

		do(t, conn, append([]string{"RPUSH", key}, elements...)...)
		expectReply(t, conn, ":"+strconv.Itoa(count)+"\r\n")
		do(t, conn, "LLEN", key)
		expectReply(t, conn, ":"+strconv.Itoa(count)+"\r\n")
	}
}

func TestLLenFollowsPushes(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":0\r\n")

	do(t, conn, "RPUSH", "list_key", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":1\r\n")

	do(t, conn, "LPUSH", "list_key", "b", "c")
	expectReply(t, conn, ":3\r\n")
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":3\r\n")

	do(t, conn, "RPUSH", "list_key", "d", "e", "f")
	expectReply(t, conn, ":6\r\n")
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":6\r\n")

	// Asking twice doesn't change the answer.
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":6\r\n")
}

func TestLLenCountsDuplicatesAndEmptyElements(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "list_key", "same", "same", "", "")
	expectReply(t, conn, ":4\r\n")
	do(t, conn, "LLEN", "list_key")
	expectReply(t, conn, ":4\r\n")
}

func TestLLenIsPerList(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "RPUSH", "short", "a")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "RPUSH", "long", "a", "b", "c", "d", "e")
	expectReply(t, conn, ":5\r\n")

	do(t, conn, "LLEN", "short")
	expectReply(t, conn, ":1\r\n")
	do(t, conn, "LLEN", "long")
	expectReply(t, conn, ":5\r\n")
}

func TestLLenIsSharedBetweenClients(t *testing.T) {
	addr := startTestServer(t)
	writer := dial(t, addr)
	reader := dial(t, addr)

	do(t, writer, "RPUSH", "list_key", "a", "b")
	expectReply(t, writer, ":2\r\n")
	do(t, reader, "LLEN", "list_key")
	expectReply(t, reader, ":2\r\n")
}

func TestLLenWrongNumberOfArguments(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "LLEN")
	expectReply(t, conn, "-ERR wrong number of arguments for 'llen' command\r\n")
	do(t, conn, "LLEN", "one", "two")
	expectReply(t, conn, "-ERR wrong number of arguments for 'llen' command\r\n")
}

func TestLLenAgainstStringIsWrongType(t *testing.T) {
	conn := dial(t, startTestServer(t))

	do(t, conn, "SET", "text", "value")
	expectReply(t, conn, "+OK\r\n")
	do(t, conn, "LLEN", "text")
	expectReply(t, conn, wrongTypeError)
}

func TestLLenIsCaseInsensitive(t *testing.T) {
	conn := dial(t, startTestServer(t))
	do(t, conn, "RPUSH", "list_key", "a", "b", "c")
	expectReply(t, conn, ":3\r\n")

	for _, name := range []string{"llen", "LLen", "lLEN"} {
		do(t, conn, name, "list_key")
		expectReply(t, conn, ":3\r\n")
	}
}
