// Package gitexec is the dependency-free leaf that shells out to the real git
// binary. It is the one place that runs git, so every other package gets
// centralized, testable git access without re-implementing the exec+stderr
// dance. We deliberately shell out rather than use a pure-Go git library:
// botfam is built on linked worktrees, runs git hooks, and relies on
// merge/--ff-only/stash/rebase — all of which need canonical git semantics
// (#318).
package gitexec

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Output runs `git <args>` in dir and returns stdout. On failure the error
// includes the command and the trimmed stderr.
func Output(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Lines runs `git <args>` in dir and returns stdout split into non-empty,
// trimmed lines.
func Lines(dir string, args ...string) ([]string, error) {
	out, err := Output(dir, args...)
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

// One runs `git <args>` in dir and returns the first non-empty output line,
// erroring when there is none.
func One(dir string, args ...string) (string, error) {
	lines, err := Lines(dir, args...)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("git %s returned no output", strings.Join(args, " "))
	}
	return lines[0], nil
}
