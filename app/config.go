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
	// dir is the directory the RDB file is stored in.
	dir string
	// dbFilename is the name of the RDB file, inside dir.
	dbFilename string
	// replicaOf is the master the server replicates, or nil if the server is
	// a master itself.
	replicaOf *masterAddress
}

// masterAddress tells a replica where its master is.
type masterAddress struct {
	host string
	port int
}

// String returns the address in the "host:port" form used to dial it.
func (m masterAddress) String() string {
	return net.JoinHostPort(m.host, strconv.Itoa(m.port))
}

// defaultConfig returns the configuration of a server started without any
// command-line argument.
func defaultConfig() config {
	return config{
		port:       defaultPort,
		dir:        ".",
		dbFilename: "dump.rdb",
	}
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
		case "dir", "dbfilename":
			if i+1 >= len(args) {
				return config{}, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			if args[i] == "" {
				return config{}, fmt.Errorf("option --%s can't be empty", name)
			}
			if name == "dir" {
				cfg.dir = args[i]
			} else {
				cfg.dbFilename = args[i]
			}
		case "replicaof":
			// The master is given as "host port", either as a single
			// argument or as two of them.
			if i+1 >= len(args) {
				return config{}, fmt.Errorf("option --%s needs a value", name)
			}
			i++
			parts := strings.Fields(args[i])
			if len(parts) == 1 && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				parts = append(parts, strings.Fields(args[i])...)
			}
			if len(parts) != 2 {
				return config{}, fmt.Errorf("option --%s needs a master host and port, such as \"localhost 6379\"", name)
			}
			port, err := parsePort(parts[1])
			if err != nil {
				return config{}, err
			}
			cfg.replicaOf = &masterAddress{host: parts[0], port: port}
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

// configParameters lists the configuration parameters clients can read with
// CONFIG GET, in the order they are reported, along with how to get their
// value.
var configParameters = []struct {
	name  string
	value func(cfg config) string
}{
	{"dbfilename", func(cfg config) string { return cfg.dbFilename }},
	{"dir", func(cfg config) string { return cfg.dir }},
	{"port", func(cfg config) string { return strconv.Itoa(cfg.port) }},
}

// cmdConfig implements the CONFIG command, whose first argument selects what
// it does:
//
//   - CONFIG GET parameter [parameter ...] replies with the configuration
//     parameters that are asked for, as an array that alternates names and
//     values. Parameters can be asked for with glob-style patterns, such as
//     "db*". Names that match nothing are left out of the reply, and a
//     parameter that is asked for several times is reported once.
//
// Subcommand and parameter names are case-insensitive.
func cmdConfig(c *client, args []string) []byte {
	switch strings.ToUpper(args[0]) {
	case "GET":
		if len(args) < 2 {
			return errorReply("ERR wrong number of arguments for 'config|get' command")
		}
		var reply []string
		for _, parameter := range configParameters {
			for _, pattern := range args[1:] {
				if globMatch(strings.ToLower(pattern), parameter.name) {
					reply = append(reply, parameter.name, parameter.value(c.server.cfg))
					break
				}
			}
		}
		return bulkStringArray(reply)
	default:
		return errorReply("ERR unknown subcommand '" + args[0] + "'. Try CONFIG HELP.")
	}
}
