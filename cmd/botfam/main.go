package main

import (
	"fmt"
	"os"

	"github.com/rlupi/botfam/internal/fam"
	"github.com/rlupi/botfam/internal/mcp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "botfam:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			return fam.Setup(os.Args[2:], os.Stdout)
		case "session":
			return fam.SessionCmd(os.Args[2:], os.Stdout)
		case "serve":
			return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
		case "-h", "--help", "help":
			printHelp()
			return nil
		default:
			return fmt.Errorf("unknown command %q", os.Args[1])
		}
	}
	return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
}

func printHelp() {
	fmt.Print(`botfam

Usage:
  botfam                  run stdio MCP server
  botfam serve            run stdio MCP server
  botfam setup <project> --agents alice,bob [--force]
  botfam session <subcommand>
`)
}
