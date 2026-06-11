package fam

import (
	"bytes"
	"os/exec"
	"strings"
)

// BuildSHA is injected at compile time via -ldflags.
// If not injected, it defaults to "dev".
var BuildSHA = "dev"

// GetVersion returns the compiled BuildSHA if it is not "dev".
// Otherwise, it attempts to query git for the current commit SHA.
// If that fails, it falls back to "dev".
func GetVersion() string {
	if BuildSHA != "dev" {
		return BuildSHA
	}

	// Runtime fallback: query git
	cmd := exec.Command("git", "rev-parse", "HEAD")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		sha := strings.TrimSpace(stdout.String())
		if sha != "" {
			return sha
		}
	}

	return "dev"
}
