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

	server := NewServerWithConfig(cfg)
	if server.isReplica() {
		// The replica serves its own clients while it follows its master.
		go func() {
			if err := server.followMaster(); err != nil {
				fmt.Println("Replication stopped:", err)
			}
		}()
	}

	if err := server.Serve(l); err != nil {
		fmt.Println("Error accepting connection: ", err.Error())
		os.Exit(1)
	}
}
