package main

import (
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

// handleConnection waits for the client to send a command and replies with a
// hardcoded PONG, whatever the command was.
func handleConnection(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 1024)
	if _, err := conn.Read(buf); err != nil {
		return
	}
	conn.Write([]byte("+PONG\r\n"))
}
