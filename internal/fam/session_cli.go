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

	"github.com/robertolupi/botfam/internal/store"
	"github.com/spf13/cobra"
)

var IsTerminal = func(fd int) bool {
	return term.IsTerminal(fd)
}

// SessionCmd is the thin args/io entry point retained for tests; it builds the
// Cobra command and runs it against args.
func SessionCmd(args []string, out io.Writer) error {
	return runCobra(NewSessionCmd(), args, out)
}

// NewSessionCmd builds the `botfam session` Cobra command and its subcommands.
func NewSessionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:           "session",
		Short:         "Manage coordination sessions (new/list/render/close/extract)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	var participants, rule, goals, guardrails string
	newCmd := &cobra.Command{
		Use:           "new <slug>",
		Short:         "Open a new session",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return sessionNew(args[0], splitCSV(participants), rule, splitCSV(goals), splitCSV(guardrails), cmd.OutOrStdout())
		},
	}
	newCmd.Flags().StringVar(&participants, "participants", "", "comma-separated participant actors")
	newCmd.Flags().StringVar(&rule, "rule", "", "consensus rule: consensus|majority|all|any")
	newCmd.Flags().StringVar(&goals, "goals", "", "comma-separated session goals")
	newCmd.Flags().StringVar(&guardrails, "guardrails", "", "comma-separated guardrails")

	listCmd := &cobra.Command{
		Use:           "list",
		Short:         "List active sessions",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return sessionList(cmd.OutOrStdout()) },
	}
	renderCmd := &cobra.Command{
		Use:           "render <slug>",
		Short:         "Render a session transcript to stdout",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return sessionRender(args[0], cmd.OutOrStdout()) },
	}
	closeCmd := &cobra.Command{
		Use:           "close <slug>",
		Short:         "Close and archive a session (operator gesture)",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, args []string) error { return sessionClose(args[0], cmd.OutOrStdout()) },
	}

	c.AddCommand(newCmd, listCmd, renderCmd, closeCmd, newSessionExtractCmd())
	return c
}

func sessionNew(slug string, participants []string, rule string, goals, guardrails []string, out io.Writer) error {
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

	if err := st.SessionNew(slug, participants, creator, rule, goals, guardrails); err != nil {
		return err
	}

	fmt.Fprintf(out, "Created session %q at %s/sessions/%s\n", slug, info.Root, slug)
	return nil
}

func sessionList(out io.Writer) error {
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

func sessionRender(slug string, out io.Writer) error {
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

func sessionClose(slug string, out io.Writer) error {
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
		wikiDir := filepath.Join(repoRoot, "wiki")
		if _, err := gitOutput(wikiDir, "add", "session-"+slug+".md"); err != nil {
			return fmt.Errorf("staging session markdown: %w", err)
		}

		// Commit interactively, allowing editing of the pre-populated commit message
		commitCmd := exec.Command("git", "commit", "-e", "-m", fmt.Sprintf("archive: close session %s", slug))
		commitCmd.Dir = wikiDir
		commitCmd.Stdin = os.Stdin
		commitCmd.Stdout = os.Stdout
		commitCmd.Stderr = os.Stderr
		if err := commitCmd.Run(); err != nil {
			return fmt.Errorf("git commit: %w", err)
		}

		fmt.Fprintf(out, "Closed, rendered, and committed session %q to wiki/session-%s.md\n", slug, slug)
	} else {
		fmt.Fprintf(out, "Closed and rendered session %q to wiki/session-%s.md\n", slug, slug)
	}
	return nil
}
