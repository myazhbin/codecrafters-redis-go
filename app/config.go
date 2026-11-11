package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// defaultPort is the port the server listens on unless told otherwise.
const defaultPort = 6379

// config holds what can be configured from the command line.
type config struct {
	// port is the TCP port the server listens on.
	port int
}

// defaultConfig returns the configuration of a server started without any
// command-line argument.
func defaultConfig() config {
	return config{port: defaultPort}
}

// parseConfig builds the server's configuration from the command-line
// arguments, without the program name. Options are given the way Redis takes
// them, as "--name value" pairs. Option names are case-insensitive.
func parseConfig(args []string) (config, error) {
	cfg := defaultConfig()

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return config{}, fmt.Errorf("unexpected argument %q", arg)
		}
		name := strings.ToLower(strings.TrimPrefix(arg, "--"))

		switch name {
		case "port":
			if i+1 >= len(args) {
				return config{}, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			port, err := parsePort(args[i])
			if err != nil {
				return config{}, err
			}
			cfg.port = port
		default:
			return config{}, fmt.Errorf("unknown option %q", arg)
		}
	}
	return cfg, nil
}

// parsePort parses a TCP port number.
func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q: must be a number between 0 and 65535", s)
	}
	return port, nil
}

// listenAddress returns the address the server listens on: every network
// interface, on the configured port.
func (cfg config) listenAddress() string {
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.port))
}
