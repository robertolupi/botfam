package fam

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/rlupi/botfam/internal/store"
)

var IsTerminal = func(fd int) bool {
	return term.IsTerminal(fd)
}

// SessionCmd dispatches session CLI subcommands.
func SessionCmd(args []string, out io.Writer) error {
	if len(args) == 0 {
		return printSessionHelp(out)
	}

	sub := args[0]
	switch sub {
	case "new":
		return sessionNew(args[1:], out)
	case "list":
		return sessionList(args[1:], out)
	case "render":
		return sessionRender(args[1:], out)
	case "close":
		return sessionClose(args[1:], out)
	case "-h", "--help", "help":
		return printSessionHelp(out)
	default:
		return fmt.Errorf("unknown session command %q", sub)
	}
}

func printSessionHelp(out io.Writer) error {
	fmt.Fprint(out, `Usage:
  botfam session new <slug> [--participants a,b]
  botfam session list
  botfam session render <slug>
  botfam session close <slug>
`)
	return nil
}

func sessionNew(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: botfam session new <slug> [--participants a,b]")
	}
	slug := args[0]
	participants := []string{}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--participants=") {
			participants = splitCSV(strings.TrimPrefix(arg, "--participants="))
		} else if arg == "--participants" {
			i++
			if i >= len(args) {
				return errors.New("--participants requires a value")
			}
			participants = splitCSV(args[i])
		} else {
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	st := store.New(info.Root)
	if err := st.Init(); err != nil {
		return err
	}

	creator := os.Getenv("COLLAB_ACTOR")
	if creator == "" {
		creator = info.Actor
	}
	if creator == "" {
		creator = "operator"
	}

	if err := st.SessionNew(slug, participants, creator); err != nil {
		return err
	}

	fmt.Fprintf(out, "Created session %q at %s/sessions/%s\n", slug, info.Root, slug)
	return nil
}

func sessionList(args []string, out io.Writer) error {
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	st := store.New(info.Root)
	list, err := st.SessionList()
	if err != nil {
		return err
	}

	if len(list) == 0 {
		fmt.Fprintln(out, "No active sessions.")
		return nil
	}

	for _, meta := range list {
		fmt.Fprintf(out, "%s (created by %s, participants: %s)\n",
			meta.Slug, meta.CreatedBy, strings.Join(meta.Participants, ", "))
	}
	return nil
}

func sessionRender(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: botfam session render <slug>")
	}
	slug := args[0]

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	st := store.New(info.Root)
	rendered, err := st.SessionRender(slug)
	if err != nil {
		return err
	}

	fmt.Fprint(out, rendered)
	return nil
}

func sessionClose(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: botfam session close <slug>")
	}
	slug := args[0]

	if os.Getenv("BOTFAM_FORCE_CLOSE") != "1" && !IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("session close is the operator's promotion gesture and requires a terminal; agents: write your closeout entry and hand back")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	st := store.New(info.Root)

	repoRoot := RepoPath(".")

	// Verify git working directory is clean (skipped if BOTFAM_FORCE_CLOSE=1)
	if os.Getenv("BOTFAM_FORCE_CLOSE") != "1" {
		statusOut, err := gitOutput(repoRoot, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("checking git status: %w", err)
		}
		if len(strings.TrimSpace(string(statusOut))) > 0 {
			return errors.New("git working directory is not clean; please commit or stash your changes before closing the session")
		}
	}

	if err := st.SessionClose(slug, repoRoot); err != nil {
		return err
	}

	// Stage and commit the rendered file (skipped if BOTFAM_FORCE_CLOSE=1)
	if os.Getenv("BOTFAM_FORCE_CLOSE") != "1" {
		relFile := filepath.Join("doc", "collab", "sessions", slug, "session.md")
		if _, err := gitOutput(repoRoot, "add", relFile); err != nil {
			return fmt.Errorf("staging session markdown: %w", err)
		}

		// Commit interactively, allowing editing of the pre-populated commit message
		commitCmd := exec.Command("git", "commit", "-e", "-m", fmt.Sprintf("archive: close session %s", slug))
		commitCmd.Dir = repoRoot
		commitCmd.Stdin = os.Stdin
		commitCmd.Stdout = os.Stdout
		commitCmd.Stderr = os.Stderr
		if err := commitCmd.Run(); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}

		fmt.Fprintf(out, "Closed, rendered, and committed session %q to doc/collab/sessions/%s/session.md\n", slug, slug)
	} else {
		fmt.Fprintf(out, "Closed and rendered session %q to doc/collab/sessions/%s/session.md\n", slug, slug)
	}
	return nil
}
