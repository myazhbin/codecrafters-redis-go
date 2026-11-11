package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Println("Invalid arguments:", err)
		os.Exit(1)
	}

	l, err := net.Listen("tcp", cfg.listenAddress())
	if err != nil {
		fmt.Printf("Failed to bind to port %d\n", cfg.port)
		os.Exit(1)
	}
	defer l.Close()

	if err := NewServerWithConfig(cfg).Serve(l); err != nil {
		fmt.Println("Error accepting connection: ", err.Error())
		os.Exit(1)
	}
}
