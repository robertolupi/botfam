package provision

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/robertolupi/botfam/internal/gitexec"
)

// InitWorktree sets a linked worktree's per-worktree git identity (user.name =
// actor, plus-addressed user.email) so commits carry the agent's identity.
func InitWorktree(args []string, out io.Writer) error {
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
	gitDir, err := gitexec.One(absPath, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	cleanGitDir := filepath.ToSlash(gitDir)
	if !strings.Contains(cleanGitDir, ".git/worktrees/") && !strings.Contains(cleanGitDir, "/.git/worktrees/") {
		return fmt.Errorf("botfam worktree init: run inside a linked worktree, not the main checkout")
	}

	fmt.Fprintf(out, "Initializing worktree config in %s for actor %s...\n", absPath, actor)

	// Enable extensions.worktreeConfig
	if _, err := gitexec.Output(absPath, "config", "extensions.worktreeConfig", "true"); err != nil {
		return fmt.Errorf("failed to set extensions.worktreeConfig: %w", err)
	}

	// Set user.name config for the worktree
	if _, err := gitexec.Output(absPath, "config", "--worktree", "user.name", actor); err != nil {
		return fmt.Errorf("failed to set user.name: %w", err)
	}

	// Determine operator name and default email dynamically from git config
	parentName, _ := gitexec.One(absPath, "config", "user.name")
	parentName = strings.TrimSpace(parentName)

	defaultEmail, _ := gitexec.One(absPath, "config", "user.email")
	if defaultEmail == "" {
		defaultEmail, _ = gitexec.One(absPath, "config", "--global", "user.email")
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

	if _, err := gitexec.Output(absPath, "config", "--worktree", "user.email", email); err != nil {
		return fmt.Errorf("failed to set user.email: %w", err)
	}

	// Print git author identity
	ident, err := gitexec.One(absPath, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return fmt.Errorf("failed to verify GIT_AUTHOR_IDENT: %w", err)
	}
	fmt.Fprintf(out, "Worktree identity successfully set:\n%s\n", ident)
	return nil
}

// SyncWorktree brings a linked worktree up to date with main: auto-stash, fast-
// forward local main to origin/main, merge main into the current branch, pop the
// stash, and sync the local wiki clone if present.
func SyncWorktree(args []string, out io.Writer) error {
	targetPath := "."
	if len(args) >= 1 {
		targetPath = args[0]
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}

	// Verify inside linked worktree
	gitDir, err := gitexec.One(absPath, "rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	cleanGitDir := filepath.ToSlash(gitDir)
	if !strings.Contains(cleanGitDir, ".git/worktrees/") && !strings.Contains(cleanGitDir, "/.git/worktrees/") {
		return fmt.Errorf("botfam worktree sync: run inside a linked worktree, not the main checkout")
	}

	// Verify per-worktree identity is set
	name, err := gitexec.One(absPath, "config", "--worktree", "user.name")
	if err != nil || strings.TrimSpace(name) == "" {
		return fmt.Errorf("no per-worktree identity set. Fix: botfam worktree init <actor> [path]")
	}

	// Verify not on detached HEAD
	branch, err := gitexec.One(absPath, "branch", "--show-current")
	if err != nil || branch == "" {
		return fmt.Errorf("detached HEAD — check out your branch first")
	}

	// Check if working tree is dirty
	dirtyLines, err := gitexec.Lines(absPath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("failed to check status: %w", err)
	}

	didStash := false
	if len(dirtyLines) > 0 {
		fmt.Fprintln(out, "Working tree is dirty. Automatically stashing local changes...")
		_, err := gitexec.Output(absPath, "stash", "push", "-u", "-m", "botfam worktree sync auto-stash")
		if err != nil {
			return fmt.Errorf("failed to stash changes: %w", err)
		}
		didStash = true
	}

	fmt.Fprintln(out, "Fetching latest changes from origin...")
	_, _ = gitexec.Output(absPath, "fetch")

	// Find the main checkout directory to fast-forward local main to origin/main
	commonDir, errCommon := gitexec.One(absPath, "rev-parse", "--git-common-dir")
	if errCommon == nil {
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Clean(filepath.Join(absPath, commonDir))
		}
		mainCheckout := commonDir
		if filepath.Base(mainCheckout) == ".git" {
			mainCheckout = filepath.Dir(mainCheckout)
		}

		// Only attempt fast-forward if origin/main exists
		_, errVerify := gitexec.One(absPath, "rev-parse", "--verify", "origin/main")
		if errVerify == nil {
			fmt.Fprintln(out, "Fast-forwarding local main to origin/main...")
			ffOut, errFF := gitexec.Output(mainCheckout, "merge", "--ff-only", "origin/main")
			if errFF != nil {
				return fmt.Errorf("local main and origin/main have diverged; cannot fast-forward: %s", strings.TrimSpace(string(ffOut)))
			}
		}
	}

	mergeTarget := "main"
	_, errVerify := gitexec.One(absPath, "rev-parse", "--verify", "main")
	if errVerify != nil {
		// Fallback to origin/main if local main somehow doesn't exist
		_, errVerifyOrigin := gitexec.One(absPath, "rev-parse", "--verify", "origin/main")
		if errVerifyOrigin == nil {
			mergeTarget = "origin/main"
		} else {
			return fmt.Errorf("neither local 'main' nor 'origin/main' found to merge")
		}
	}

	fmt.Fprintf(out, "Merging %s into branch %q...\n", mergeTarget, branch)
	mergeOut, err := gitexec.Output(absPath, "merge", mergeTarget)
	if err != nil {
		// If merge fails, print merge output and return error.
		// Note that we don't pop the stash if there are conflicts.
		fmt.Fprint(out, string(mergeOut))
		return fmt.Errorf("merge failed: resolve conflicts manually, then pop stash if applicable: %w", err)
	}
	fmt.Fprint(out, string(mergeOut))

	if didStash {
		fmt.Fprintln(out, "Popping stashed local changes...")
		popOut, err := gitexec.Output(absPath, "stash", "pop")
		if err != nil {
			fmt.Fprintln(out, string(popOut))
			return fmt.Errorf("stash pop failed (merge succeeded): %w", err)
		}
		fmt.Fprint(out, string(popOut))
	}

	lastCommit, err := gitexec.One(absPath, "log", "--oneline", "-1")
	if err == nil {
		fmt.Fprintf(out, "HEAD is now at: %s\n", lastCommit)
	}

	// Sync the Gitea wiki if present (issue #82)
	wikiDir := filepath.Join(absPath, "wiki")
	if info, err := os.Stat(filepath.Join(wikiDir, ".git")); err == nil && info.IsDir() {
		fmt.Fprintln(out, "Syncing local wiki clone...")
		wikiDirty, _ := gitexec.Lines(wikiDir, "status", "--porcelain")
		wikiStashed := false
		if len(wikiDirty) > 0 {
			fmt.Fprintln(out, "  Wiki has local changes. Stashing...")
			if _, err := gitexec.Output(wikiDir, "stash", "push", "-u", "-m", "botfam wiki sync auto-stash"); err == nil {
				wikiStashed = true
			}
		}

		fmt.Fprintln(out, "  Fetching and pulling latest wiki changes...")
		if pullOut, err := gitexec.Output(wikiDir, "pull", "--rebase"); err != nil {
			fmt.Fprintf(out, "  warning: wiki pull failed: %v\n%s", err, string(pullOut))
		} else {
			fmt.Fprint(out, string(pullOut))
		}

		if wikiStashed {
			fmt.Fprintln(out, "  Popping stashed wiki changes...")
			if popOut, err := gitexec.Output(wikiDir, "stash", "pop"); err != nil {
				fmt.Fprintf(out, "  warning: wiki stash pop failed: %v\n%s", err, string(popOut))
			}
		}
	}

	return nil
}
