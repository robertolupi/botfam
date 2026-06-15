package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// Resolver and RootInfo now live in the dependency-free famconfig leaf (#311) so
// internal/cli and internal/mcp resolve fam identity without importing each
// other. These aliases keep the cli command builders unaffected by the move.
type Resolver = famconfig.Resolver

type RootInfo = famconfig.RootInfo

// ResolveRepoName re-exports famconfig.ResolveRepoName.
func ResolveRepoName(workDir string) string { return famconfig.ResolveRepoName(workDir) }

// ParseActor re-exports famconfig.ParseActor.
func ParseActor(base string, repoName string) string { return famconfig.ParseActor(base, repoName) }

// GitObjectStores re-exports famconfig.GitObjectStores.
func GitObjectStores(workDir string) ([]string, error) { return famconfig.GitObjectStores(workDir) }

// RepoPath re-exports famconfig.RepoPath.
func RepoPath(workDir string) string { return famconfig.RepoPath(workDir) }

// ValidateHistoryPath re-exports famconfig.ValidateHistoryPath.
func ValidateHistoryPath(path string) error { return famconfig.ValidateHistoryPath(path) }

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

var jsonOutput bool

func IsJSONOutput() bool {
	return jsonOutput
}

func SetJSONOutput(v bool) {
	jsonOutput = v
}

func writeJSONOutput(out io.Writer, val any) error {
	w := json.NewEncoder(out)
	return w.Encode(map[string]any{
		"ok":     true,
		"result": val,
	})
}

func writeJSONError(out io.Writer, err error) error {
	w := json.NewEncoder(out)
	return w.Encode(map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}
