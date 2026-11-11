package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	defer l.Close()

	conn, err := l.Accept()
	if err != nil {
		fmt.Println("Error accepting connection: ", err.Error())
		os.Exit(1)
	}
	handleConnection(conn)
}

// handleConnection reads commands from the client one after another until it
// disconnects, replying to each of them with a hardcoded PONG.
func handleConnection(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	for {
		if _, err := readCommand(reader); err != nil {
			if errors.Is(err, errProtocol) {
				fmt.Fprintf(conn, "-ERR %s\r\n", err)
			}
			return
		}
		conn.Write([]byte("+PONG\r\n"))
	}
}
