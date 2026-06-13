package fam

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// WorktreeCmd handles "botfam worktree <init|sync> [args]"
func WorktreeCmd(args []string, out io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: botfam worktree <init|sync> [args]")
	}
	switch args[0] {
	case "init":
		return worktreeInit(args[1:], out)
	case "sync":
		return worktreeSync(args[1:], out)
	default:
		return fmt.Errorf("unknown worktree subcommand %q", args[0])
	}
}

func worktreeInit(args []string, out io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: botfam worktree init <actor> [path]")
	}
	actor := args[0]
	targetPath := "."
	if len(args) >= 2 {
		targetPath = args[1]
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}

	// Verify it's a linked worktree
	gitDir, err := gitOne(absPath, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	cleanGitDir := filepath.ToSlash(gitDir)
	if !strings.Contains(cleanGitDir, ".git/worktrees/") && !strings.Contains(cleanGitDir, "/.git/worktrees/") {
		return fmt.Errorf("botfam worktree init: run inside a linked worktree, not the main checkout")
	}

	fmt.Fprintf(out, "Initializing worktree config in %s for actor %s...\n", absPath, actor)

	// Enable extensions.worktreeConfig
	if _, err := gitOutput(absPath, "config", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("failed to set extensions.worktreeConfig: %w", err)
	}

	// Set user.name config for the worktree
	if _, err := gitOutput(absPath, "config", "--worktree", "user.name", actor); err != nil {
		return fmt.Errorf("failed to set user.name: %w", err)
	}

	// Set user.email config for the worktree
	var email string
	if actor == "rlupi" {
		email = "roberto.lupi@gmail.com"
	} else {
		email = fmt.Sprintf("roberto.lupi+%s@gmail.com", actor)
	}
	if _, err := gitOutput(absPath, "config", "--worktree", "user.email", email); err != nil {
		return fmt.Errorf("failed to set user.email: %w", err)
	}

	// Print git author identity
	ident, err := gitOne(absPath, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return fmt.Errorf("failed to verify GIT_AUTHOR_IDENT: %w", err)
	}
	fmt.Fprintf(out, "Worktree identity successfully set:\n%s\n", ident)
	return nil
}

func worktreeSync(args []string, out io.Writer) error {
	targetPath := "."
	if len(args) >= 1 {
		targetPath = args[0]
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}

	// Verify inside linked worktree
	gitDir, err := gitOne(absPath, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	cleanGitDir := filepath.ToSlash(gitDir)
	if !strings.Contains(cleanGitDir, ".git/worktrees/") && !strings.Contains(cleanGitDir, "/.git/worktrees/") {
		return fmt.Errorf("botfam worktree sync: run inside a linked worktree, not the main checkout")
	}

	// Verify per-worktree identity is set
	name, err := gitOne(absPath, "config", "--worktree", "user.name")
	if err != nil || strings.TrimSpace(name) == "" {
		return fmt.Errorf("no per-worktree identity set. Fix: botfam worktree init <actor> [path]")
	}

	// Verify not on detached HEAD
	branch, err := gitOne(absPath, "branch", "--show-current")
	if err != nil || branch == "" {
		return fmt.Errorf("detached HEAD — check out your branch first")
	}

	// Check if working tree is dirty
	dirtyLines, err := gitLines(absPath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("failed to check status: %w", err)
	}

	didStash := false
	if len(dirtyLines) > 0 {
		fmt.Fprintln(out, "Working tree is dirty. Automatically stashing local changes...")
		_, err := gitOutput(absPath, "stash", "push", "-u", "-m", "botfam worktree sync auto-stash")
		if err != nil {
			return fmt.Errorf("failed to stash changes: %w", err)
		}
		didStash = true
	}

	fmt.Fprintf(out, "Merging main into branch %q...\n", branch)
	mergeOut, err := gitOutput(absPath, "merge", "main")
	if err != nil {
		// If merge fails, print merge output and return error.
		// Note that we don't pop the stash if there are conflicts.
		fmt.Fprintln(out, string(mergeOut))
		return fmt.Errorf("merge failed: resolve conflicts manually, then pop stash if applicable: %w", err)
	}
	fmt.Fprint(out, string(mergeOut))

	if didStash {
		fmt.Fprintln(out, "Popping stashed local changes...")
		popOut, err := gitOutput(absPath, "stash", "pop")
		if err != nil {
			fmt.Fprintln(out, string(popOut))
			return fmt.Errorf("stash pop failed (merge succeeded): %w", err)
		}
		fmt.Fprint(out, string(popOut))
	}

	lastCommit, err := gitOne(absPath, "log", "--oneline", "-1")
	if err == nil {
		fmt.Fprintf(out, "HEAD is now at: %s\n", lastCommit)
	}

	return nil
}
