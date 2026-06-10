package server

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// getProcessCWD returns the CWD of the given PID.
func getProcessCWD(pid int) (string, error) {
	if _, err := os.Stat("/proc"); err == nil {
		// Linux-like system
		return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	}

	// Darwin/macOS (using lsof)
	cmd := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-F", "n")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("lsof failed: %w", err)
	}

	lines := strings.Split(out.String(), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n"), nil
		}
	}
	return "", fmt.Errorf("cwd not found in lsof output: %s", out.String())
}

// findGitRoot walks up the directory tree to find a git repository root.
func findGitRoot(path string) (string, error) {
	curr, err := filepath.Abs(path)
	if err != nil {
		curr = path
	}
	curr = filepath.Clean(curr)
	for {
		gitPath := filepath.Join(curr, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return curr, nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", fmt.Errorf("git root not found for path %s", path)
}
