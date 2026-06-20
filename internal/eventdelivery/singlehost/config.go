package singlehost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// SessionFile holds the configuration details for a live sprint session.
// Marshaled to and from ~/.botfam/sprint-session-<repo>.config.toml.
type SessionFile struct {
	LeaseID      string `toml:"lease_id"`
	FencingToken uint64 `toml:"fencing_token"`
	PID          int    `toml:"pid"`
	Addr         string `toml:"addr"`
	Token        string `toml:"token"`
}

// ConfigPath returns the path to the sprint session file for a repository.
func ConfigPath(repoName string) (string, error) {
	if repoName == "" {
		return "", fmt.Errorf("repository name cannot be empty")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}
	dir := filepath.Join(home, ".botfam")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create .botfam dir: %w", err)
	}
	return filepath.Join(dir, fmt.Sprintf("sprint-session-%s.config.toml", repoName)), nil
}

// isProcessLive checks if the process is running using flock/PID/command checks.
func isProcessLive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we must send signal 0
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}

	// Double-check with ps -p <pid> -o comm= to avoid PID reuse confusion
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	comm := strings.TrimSpace(string(out))
	baseComm := filepath.Base(comm)

	execPath, err := os.Executable()
	if err != nil {
		return strings.Contains(comm, "botfam")
	}
	execBase := filepath.Base(execPath)

	return strings.Contains(baseComm, "botfam") || baseComm == execBase || strings.Contains(baseComm, execBase)
}
