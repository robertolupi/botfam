package harness

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Small dependency-free git helpers for the per-worktree git-identity render.
// The harness leaf must not import internal/fam (which would cycle), so it keeps
// its own copies rather than borrowing fam's.

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

func gitOne(workDir string, args ...string) (string, error) {
	out, err := gitOutput(workDir, args...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			return line, nil
		}
	}
	return "", fmt.Errorf("git %s returned no output", strings.Join(args, " "))
}
