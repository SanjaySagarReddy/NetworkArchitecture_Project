package main

import (
	"fmt"
	"os"
)

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s [flags] <root_dir> <port>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Flags:\n")
	fmt.Fprintf(os.Stderr, "  --inject-unknown  Inject frame of type 0x7F before every RESPONSE\n")
	fmt.Fprintf(os.Stderr, "  -v                Hexdump every frame sent/received to stderr\n")
}

func main() {
	var (
		injectUnknown bool
		verbose       bool
		positional    []string
	)

	for _, arg := range os.Args[1:] {
		switch arg {
		case "--inject-unknown", "-inject-unknown":
			injectUnknown = true
		case "-v", "--v":
			verbose = true
		case "-h", "--help", "-help":
			printUsage()
			os.Exit(0)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
				printUsage()
				os.Exit(1)
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) != 2 {
		printUsage()
		os.Exit(1)
	}

	rootDir := positional[0]
	port := positional[1]

	srv, err := NewServer(rootDir, port, injectUnknown, verbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := srv.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
