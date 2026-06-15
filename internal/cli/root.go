package cli

import (
	"encoding/json"
	"io"

	"github.com/robertolupi/botfam/internal/famconfig"
)

type Resolver = famconfig.Resolver

type GitResolver = famconfig.GitResolver

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
