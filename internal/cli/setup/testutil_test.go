package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// initGitRepo creates a minimal git repository at dir with one committed commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s %v: %v", name, args, err)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "test")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "commit", "--allow-empty", "-m", "initial commit")
}

// gitInit creates a minimal git repository at dir (with subdirectory creation).
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// deepCutsConfig is the canonical test config: the deep-cuts fam ([repo.deep-cuts]
// slug dc) registered at famDir, with the standard roster.
func deepCutsConfig(famDir string) famconfig.Config {
	return famconfig.Config{
		ForgeURL: "http://gitea.home.rlupi.com:3000/",
		Agents: map[string]famconfig.AgentConfig{
			"claude": {Harness: "claude-code", ForgeUser: "claude-bot"},
			"agy":    {Harness: "antigravity", ForgeUser: "agy-bot", Email: "roberto.lupi+agy@gmail.com"},
		},
		Users: map[string]famconfig.AgentConfig{"rlupi": {ForgeUser: "rlupi"}},
		Repos: map[string]famconfig.RepoConfig{
			"deep-cuts": {Path: famDir, Slug: "dc", Repository: "deep-cuts/deep-cuts"},
		},
	}
}

// resolveFamFixture points BOTFAM_CONFIG at a temp config registering the
// deep-cuts fam at a fresh famDir, and returns famDir. Callers git-init the
// worktrees under it.
func resolveFamFixture(t *testing.T) (famDir string) {
	t.Helper()
	famDir = t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	cfgDir := t.TempDir()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(cfgDir, "config.toml"))
	if err := famconfig.WriteConfig(deepCutsConfig(famDir)); err != nil {
		t.Fatal(err)
	}
	return famDir
}
