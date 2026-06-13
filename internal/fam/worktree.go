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

	// Determine operator name and default email dynamically from git config
	parentName, _ := gitOne(absPath, "config", "user.name")
	parentName = strings.TrimSpace(parentName)

	defaultEmail, _ := gitOne(absPath, "config", "user.email")
	if defaultEmail == "" {
		defaultEmail, _ = gitOne(absPath, "config", "--global", "user.email")
	}
	defaultEmail = strings.TrimSpace(defaultEmail)

	var email string
	if defaultEmail != "" {
		if idx := strings.Index(defaultEmail, "@"); idx != -1 {
			local := defaultEmail[:idx]
			domain := defaultEmail[idx:]
			if actor == parentName || parentName == "" {
				email = defaultEmail
			} else {
				suffix := "+" + actor
				if strings.HasSuffix(local, suffix) {
					email = defaultEmail
				} else {
					email = local + suffix + domain
				}
			}
		} else {
			email = defaultEmail
		}
	} else {
		email = fmt.Sprintf("%s@localhost", actor)
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

	fmt.Fprintln(out, "Fetching latest changes from origin...")
	_, _ = gitOutput(absPath, "fetch")

	// Find the main checkout directory to fast-forward local main to origin/main
	commonDir, errCommon := gitOne(absPath, "rev-parse", "--git-common-dir")
	if errCommon == nil {
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Clean(filepath.Join(absPath, commonDir))
		}
		mainCheckout := commonDir
		if filepath.Base(mainCheckout) == ".git" {
			mainCheckout = filepath.Dir(mainCheckout)
		}

		// Only attempt fast-forward if origin/main exists
		_, errVerify := gitOne(absPath, "rev-parse", "--verify", "origin/main")
		if errVerify == nil {
			fmt.Fprintln(out, "Fast-forwarding local main to origin/main...")
			ffOut, errFF := gitOutput(mainCheckout, "merge", "--ff-only", "origin/main")
			if errFF != nil {
				return fmt.Errorf("local main and origin/main have diverged; cannot fast-forward: %s", strings.TrimSpace(string(ffOut)))
			}
		}
	}

	mergeTarget := "main"
	_, errVerify := gitOne(absPath, "rev-parse", "--verify", "main")
	if errVerify != nil {
		// Fallback to origin/main if local main somehow doesn't exist
		_, errVerifyOrigin := gitOne(absPath, "rev-parse", "--verify", "origin/main")
		if errVerifyOrigin == nil {
			mergeTarget = "origin/main"
		} else {
			return fmt.Errorf("neither local 'main' nor 'origin/main' found to merge")
		}
	}

	fmt.Fprintf(out, "Merging %s into branch %q...\n", mergeTarget, branch)
	mergeOut, err := gitOutput(absPath, "merge", mergeTarget)
	if err != nil {
		// If merge fails, print merge output and return error.
		// Note that we don't pop the stash if there are conflicts.
		fmt.Fprint(out, string(mergeOut))
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
