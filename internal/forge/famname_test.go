package forge

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo creates a minimal git repo at dir with one commit so that
// `git rev-parse --show-toplevel` resolves dir as the worktree root.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestResolveFamName_FromRootAndSubdir(t *testing.T) {
	t.Setenv("BOTFAM_FAM", "")
	// Layout: <tmp>/myfam/repo is the worktree; "myfam" is the fam name.
	famDir := filepath.Join(t.TempDir(), "myfam")
	repo := filepath.Join(famDir, "repo")
	initGitRepo(t, repo)

	subdir := filepath.Join(repo, "internal", "fam")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		workDir string
	}{
		{"root", repo},
		{"subdir", subdir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveFamName(tc.workDir); got != "myfam" {
				t.Errorf("resolveFamName(%q) = %q, want %q", tc.workDir, got, "myfam")
			}
		})
	}
}

func TestResolveFamName_EnvOverride(t *testing.T) {
	t.Setenv("BOTFAM_FAM", "deep-cuts")
	if got := resolveFamName("/wherever/it/does/not/matter"); got != "deep-cuts" {
		t.Errorf("resolveFamName with BOTFAM_FAM = %q, want %q", got, "deep-cuts")
	}
}
