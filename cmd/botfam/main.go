package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rlupi/botfam/internal/fam"
	"github.com/rlupi/botfam/internal/mcp"
	"github.com/rlupi/botfam/internal/server"
)

func main() {
	if err := run(); err != nil {
		if fam.IsJSONOutput() {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"ok":    false,
				"error": err.Error(),
			})
		} else {
			fmt.Fprintln(os.Stderr, "botfam:", err)
		}
		os.Exit(1)
	}
}

func run() error {
	// Parse global --json flag
	var cleanArgs []string
	isJSON := false
	for _, arg := range os.Args {
		if arg == "--json" || arg == "-j" {
			isJSON = true
		} else {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	os.Args = cleanArgs
	fam.SetJSONOutput(isJSON)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			return fam.Setup(os.Args[2:], os.Stdout)
		case "session":
			return fam.SessionCmd(os.Args[2:], os.Stdout)
		case "topic":
			return fam.TopicCmd(os.Args[2:], os.Stdout)
		case "merge-gate":
			return fam.MergeGateCmd(os.Args[2:], os.Stdout)
		case "agent-docs":
			return fam.AgentDocsCmd(os.Args[2:], os.Stdout)
		case "vote":
			return fam.VoteCmd(os.Args[2:], os.Stdout)
		case "tally":
			return fam.TallyCmd(os.Args[2:], os.Stdout)
		case "propose":
			return fam.ProposeCmd(os.Args[2:], os.Stdout)
		case "approve":
			return fam.ApproveCmd(os.Args[2:], os.Stdout)
		case "merge":
			return fam.MergeCmd(os.Args[2:], os.Stdout)
		case "server":
			var udsPath string
			tcpPort := 8080
			for i := 2; i < len(os.Args); i++ {
				arg := os.Args[i]
				if arg == "--socket" || arg == "-s" {
					i++
					if i < len(os.Args) {
						udsPath = os.Args[i]
					}
				} else if strings.HasPrefix(arg, "--socket=") {
					udsPath = strings.TrimPrefix(arg, "--socket=")
				} else if arg == "--port" || arg == "-p" {
					i++
					if i < len(os.Args) {
						var err error
						tcpPort, err = strconv.Atoi(os.Args[i])
						if err != nil {
							return fmt.Errorf("invalid port: %w", err)
						}
					}
				} else if strings.HasPrefix(arg, "--port=") {
					var err error
					tcpPort, err = strconv.Atoi(strings.TrimPrefix(arg, "--port="))
					if err != nil {
						return fmt.Errorf("invalid port: %w", err)
					}
				} else {
					return fmt.Errorf("unknown server argument %q", arg)
				}
			}

			if udsPath == "" {
				if envPath := os.Getenv("BOTFAM_SOCKET"); envPath != "" {
					udsPath = envPath
				} else {
					home, err := os.UserHomeDir()
					if err != nil {
						return err
					}
					udsPath = filepath.Join(home, ".botfam", "daemon.sock")
					if len(udsPath) > 104 {
						h := sha256.Sum256([]byte(home))
						udsPath = filepath.Join("/tmp", fmt.Sprintf("bf-%s.sock", hex.EncodeToString(h[:])))
					}
				}
			}

			srv := server.NewServer(udsPath, tcpPort)
			fmt.Printf("Starting botfam server UDS daemon on %s, HTTP/SSE on localhost:%d\n", udsPath, tcpPort)
			return srv.Start(context.Background())
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
  botfam topic <subcommand>
  botfam merge-gate --commit <sha> --proposal <id>
  botfam agent-docs generate|check
  botfam server [--socket <path>] [--port <port>]
  botfam vote --proposal <id> --verdict <verdict>
  botfam tally --proposal <id>
  botfam propose --proposal <id> [--quorum <quorum>] [--deadline <deadline>]
  botfam approve --proposal <id> [--verdict <verdict>]
  botfam merge --proposal <id>

Global Flags:
  --json, -j              output results as structured JSON lines
`)
}
