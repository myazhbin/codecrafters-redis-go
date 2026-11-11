package main

import (
	"bufio"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  [][]string // every command expected before the stream ends
	}{
		{
			name:  "array with a single bulk string",
			input: "*1\r\n$4\r\nPING\r\n",
			want:  [][]string{{"PING"}},
		},
		{
			name:  "array with arguments",
			input: "*2\r\n$4\r\nECHO\r\n$3\r\nhey\r\n",
			want:  [][]string{{"ECHO", "hey"}},
		},
		{
			name:  "several commands in one buffer",
			input: "*1\r\n$4\r\nPING\r\n*1\r\n$4\r\nPING\r\n*2\r\n$4\r\nECHO\r\n$2\r\nhi\r\n",
			want:  [][]string{{"PING"}, {"PING"}, {"ECHO", "hi"}},
		},
		{
			name:  "bulk string containing CRLF and spaces",
			input: "*2\r\n$4\r\nECHO\r\n$7\r\na\r\nb  c\r\n",
			want:  [][]string{{"ECHO", "a\r\nb  c"}},
		},
		{
			name:  "empty bulk string",
			input: "*2\r\n$4\r\nECHO\r\n$0\r\n\r\n",
			want:  [][]string{{"ECHO", ""}},
		},
		{
			name:  "inline command",
			input: "PING\r\n",
			want:  [][]string{{"PING"}},
		},
		{
			name:  "inline command with arguments and a bare LF",
			input: "ECHO   hello\n",
			want:  [][]string{{"ECHO", "hello"}},
		},
		{
			name:  "empty lines and empty arrays are skipped",
			input: "\r\n*0\r\n*-1\r\n*1\r\n$4\r\nPING\r\n",
			want:  [][]string{{"PING"}},
		},
		{
			name:  "no input",
			input: "",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))

			for i, want := range tt.want {
				got, err := readCommand(r)
				if err != nil {
					t.Fatalf("command %d: unexpected error: %v", i, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("command %d = %q, want %q", i, got, want)
				}
			}

			if got, err := readCommand(r); err != io.EOF {
				t.Fatalf("after the last command: got (%q, %v), want io.EOF", got, err)
			}
		})
	}
}

func TestReadCommandErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		protocol bool // whether the error must be a protocol error
	}{
		{name: "truncated array header", input: "*1"},
		{name: "missing elements", input: "*2\r\n$4\r\nECHO\r\n"},
		{name: "truncated bulk header", input: "*1\r\n$4"},
		{name: "truncated bulk payload", input: "*1\r\n$4\r\nPI"},
		{name: "missing final CRLF", input: "*1\r\n$4\r\nPING"},
		{name: "non numeric array length", input: "*abc\r\n", protocol: true},
		{name: "oversized array length", input: "*1048577\r\n", protocol: true},
		{name: "element is not a bulk string", input: "*1\r\n+PING\r\n", protocol: true},
		{name: "non numeric bulk length", input: "*1\r\n$x\r\nPING\r\n", protocol: true},
		{name: "negative bulk length", input: "*1\r\n$-1\r\n", protocol: true},
		{name: "bulk longer than announced", input: "*1\r\n$2\r\nPING\r\n", protocol: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))

			got, err := readCommand(r)
			if err == nil {
				t.Fatalf("got command %q, want an error", got)
			}
			if err == io.EOF {
				t.Fatalf("got io.EOF, which is reserved for a clean end of stream")
			}
			if isProtocol := errors.Is(err, errProtocol); isProtocol != tt.protocol {
				t.Fatalf("errors.Is(err, errProtocol) = %v, want %v (err: %v)", isProtocol, tt.protocol, err)
			}
		})
	}
}

func TestReplyEncoding(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{"simple string", simpleString("PONG"), "+PONG\r\n"},
		{"error", errorReply("ERR something went wrong"), "-ERR something went wrong\r\n"},
		{"integer", integerReply(42), ":42\r\n"},
		{"zero", integerReply(0), ":0\r\n"},
		{"negative integer", integerReply(-7), ":-7\r\n"},
		{"bulk string", bulkString("hey"), "$3\r\nhey\r\n"},
		{"empty bulk string", bulkString(""), "$0\r\n\r\n"},
		{"bulk string length is in bytes", bulkString("héllo"), "$6\r\nhéllo\r\n"},
		{"bulk string with CRLF", bulkString("a\r\nb"), "$4\r\na\r\nb\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Fatalf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}
