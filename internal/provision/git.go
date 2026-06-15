// Package provision is the dependency-free leaf for fam/worktree lifecycle
// operations: initializing a worktree's git identity, syncing it with main,
// registering worktrees into the fam registry, and verifying fam membership.
// internal/cli (the worktree/setup commands) and internal/mcp (the
// worktree_init/worktree_sync tools) both call it, so neither imports the
// other (#311).
package provision

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func gitOutput(workDir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func gitLines(workDir string, args ...string) ([]string, error) {
	out, err := gitOutput(workDir, args...)
	if err != nil {
		return nil, err
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func gitOne(workDir string, args ...string) (string, error) {
	lines, err := gitLines(workDir, args...)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("git %s returned no output", strings.Join(args, " "))
	}
	return lines[0], nil
}

func unique(xs []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
