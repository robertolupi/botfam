package singlehost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
)

// SessionFile holds the configuration details for a live sprint session.
// Marshaled to and from ~/.botfam/sprint-session-<repo>.config.toml.
type SessionFile struct {
	LeaseID      string `toml:"lease_id"`
	FencingToken uint64 `toml:"fencing_token"`
	PID          int    `toml:"pid"`
	Addr         string `toml:"addr"`
	Token        string `toml:"token"`
	// SessionID is the sprint session id the live supervisor is running. Empty
	// until the supervisor stamps it via SetEndpoint; consumers that need to act
	// on a specific session (e.g. `sprint end`) must treat empty as unproven.
	SessionID string `toml:"session_id"`
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

// LiveSupervisorPID returns the PID and stamped sprint session id of the live
// supervisor holding the lease for repoName, or ok=false if the session file is
// absent or its process is no longer live. The session id is empty until the
// supervisor stamps it (via SetEndpoint); callers acting on a specific session
// must treat an empty/mismatched id as "not this session". Read-only.
func LiveSupervisorPID(repoName string) (pid int, sessionID string, ok bool) {
	path, err := ConfigPath(repoName)
	if err != nil {
		return 0, "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", false
	}
	var sf SessionFile
	if err := toml.Unmarshal(data, &sf); err != nil {
		return 0, "", false
	}
	if !isProcessLive(sf.PID) {
		return 0, "", false
	}
	return sf.PID, sf.SessionID, true
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
		// If ps fails (e.g. not found, BusyBox on Alpine/Docker, etc.),
		// fallback to the Signal(0) success.
		return true
	}
	outStr := string(out)
	var comm string
	for _, line := range strings.Split(outStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ToUpper(line) == "COMMAND" {
			continue
		}
		comm = line
		break
	}
	baseComm := filepath.Base(comm)

	execPath, err := os.Executable()
	var execBase string
	if err == nil {
		execBase = filepath.Base(execPath)
	} else {
		execBase = filepath.Base(os.Args[0])
	}
	if execBase == "" {
		execBase = filepath.Base(os.Args[0])
	}

	if strings.Contains(baseComm, "botfam") || (execBase != "" && strings.Contains(execBase, "botfam")) {
		return true
	}
	if len(baseComm) >= 3 && execBase != "" && strings.Contains(execBase, baseComm) {
		return true
	}
	if execBase != "" && len(execBase) >= 3 && strings.Contains(baseComm, execBase) {
		return true
	}
	return false
}
