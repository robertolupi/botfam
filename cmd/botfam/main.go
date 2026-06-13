package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/robertolupi/botfam/internal/fam"
	"github.com/robertolupi/botfam/internal/mcp"
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
		case "version":
			fmt.Fprintln(os.Stdout, fam.GetVersion())
			return nil
		case "worktree":
			return fam.WorktreeCmd(os.Args[2:], os.Stdout)
		case "setup":
			return fam.Setup(os.Args[2:], os.Stdout)
		case "newfam":
			return fam.NewfamCmd(os.Args[2:], os.Stdout)
		case "session":
			return fam.SessionCmd(os.Args[2:], os.Stdout)
		case "verify":
			return fam.VerifyCmd(os.Args[2:], os.Stdout)
		case "agent-docs":
			return fam.AgentDocsCmd(os.Args[2:], os.Stdout)
		case "irc-client":
			return fam.IrcClientCmd(os.Args[2:], os.Stdout)
		case "irc-wait":
			return fam.IrcWaitCmd(os.Args[2:], os.Stdout)
		case "forge-wait":
			return fam.ForgeWaitCmd(os.Args[2:], os.Stdout)
		case "external-review":
			return fam.ExternalReviewCmd(os.Args[2:], os.Stdout)
		case "scribe":
			return fam.ScribeCmd(os.Args[2:], os.Stdout)
		case "irclog2sessions":
			return fam.IrcLog2SessionsCmd(os.Args[2:], os.Stdout)
		case "serve":
			return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
		case "-h", "--help", "help":
			printHelp()
			return nil
		default:
			return fmt.Errorf("unknown command %q", os.Args[1])
		}
	} else {
		if !isTerminal(os.Stdin) && !isTerminal(os.Stdout) {
			return mcp.Serve(os.Stdin, os.Stdout, os.Stderr)
		}
	}
	printHelp()
	return nil
}

func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func printHelp() {
	fmt.Print(`botfam

Usage:
  botfam version          print build version/SHA
  botfam serve            run stdio MCP server
  botfam worktree <init|sync|register> [args]
  botfam setup <project> --agents alice,bob [--force]
  botfam newfam <project> --agents alice,bob
  botfam session <subcommand>
  botfam verify <sha> [pkgs...]
  botfam agent-docs generate|check
  botfam irc-client <nick> [--server <host:port>] [--channel <channel>] [--dir <dir>] [--pass-file <file>]
  botfam irc-wait --nick <nick> [--file <path>]
  botfam forge-wait [--once] [--interval <s>] [--timeout <s>] [--mark-read]
  botfam external-review [--pr <index>] --ollama|--openai|--gemini <model> [MATERIAL...]
  botfam scribe [--server <host:port>] [--channel <channel>] [--file <path>]
  botfam irclog2sessions <chat.log>... [--out <dir>] [--gap-minutes <n>] [--channel <chan>]... [--include-open]

Global Flags:
  --json, -j              output results as structured JSON lines
`)
}
