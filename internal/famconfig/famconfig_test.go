package famconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const sampleFamTOML = `name       = "deep-cuts"
slug       = "dc"
forge_url  = "http://gitea.home.rlupi.com:3000/"
repository = "deep-cuts/deep-cuts"
roster     = ["claude", "rlupi"]

[agent.claude]
harness    = "claude-code"
forge_user = "claude-bot"

[user.rlupi]
forge_user = "rlupi"
`

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

// famFixture writes fam.toml at famDir and git-inits famDir/<worktree>.
func famFixture(t *testing.T) (famDir string) {
	t.Helper()
	famDir = t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	if err := os.WriteFile(filepath.Join(famDir, "fam.toml"), []byte(sampleFamTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	return famDir
}

func TestReadRegistryBackfillsKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fam.toml")
	if err := os.WriteFile(path, []byte(sampleFamTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if reg.Agents["claude"].Name != "claude" || reg.Agents["claude"].Harness != "claude-code" {
		t.Errorf("agent.claude = %+v", reg.Agents["claude"])
	}
	if u := reg.Users["rlupi"]; !u.IsUser || u.Name != "rlupi" {
		t.Errorf("user.rlupi = %+v", u)
	}
	if FamSlug(reg) != "dc" {
		t.Errorf("FamSlug = %q, want dc", FamSlug(reg))
	}
}

func TestHarnessTokenPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := HarnessTokenPath("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".botfam", "token-claude-code"); got != want {
		t.Errorf("HarnessTokenPath = %q, want %q", got, want)
	}
	if _, err := HarnessTokenPath(""); err == nil {
		t.Error("empty harness should error")
	}
}

func TestFindFamTOMLPath(t *testing.T) {
	famDir := famFixture(t)
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)
	want := filepath.Join(famDir, "fam.toml")

	// Parent-of-toplevel branch (no COLLAB_ROOT).
	if got := FindFamTOMLPath(wt, []string{}); got != want {
		t.Errorf("parent-of-toplevel: got %q, want %q", got, want)
	}

	// COLLAB_ROOT override wins when it has a fam.toml.
	if got := FindFamTOMLPath(wt, []string{"COLLAB_ROOT=" + famDir}); got != want {
		t.Errorf("COLLAB_ROOT: got %q, want %q", got, want)
	}

	// COLLAB_ROOT pointing at a dir with no fam.toml falls through to parent.
	if got := FindFamTOMLPath(wt, []string{"COLLAB_ROOT=" + t.TempDir()}); got != want {
		t.Errorf("COLLAB_ROOT-no-toml fallthrough: got %q, want %q", got, want)
	}

	// A non-git dir with no env yields "".
	if got := FindFamTOMLPath(t.TempDir(), []string{}); got != "" {
		t.Errorf("non-git: got %q, want empty", got)
	}
}

func TestResolveFam(t *testing.T) {
	famDir := famFixture(t)

	// Declared agent resolves.
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)
	rf, err := ResolveFam(wt)
	if err != nil {
		t.Fatalf("ResolveFam(agent): %v", err)
	}
	if rf.Actor != "claude" || rf.Slug != "dc" || rf.ForgeURL != "http://gitea.home.rlupi.com:3000/" || rf.Repository != "deep-cuts/deep-cuts" {
		t.Errorf("ResolvedFam = %+v", rf)
	}
	home, _ := os.UserHomeDir()
	if rf.TokenPath != filepath.Join(home, ".botfam", "token-claude-code") {
		t.Errorf("TokenPath = %q", rf.TokenPath)
	}

	// User worktree is refused.
	user := filepath.Join(famDir, "rlupi")
	gitInit(t, user)
	if _, err := ResolveFam(user); err == nil {
		t.Error("expected refusal for a [user.<name>] worktree")
	}

	// Base/unknown checkout is refused.
	main := filepath.Join(famDir, "main")
	gitInit(t, main)
	if _, err := ResolveFam(main); err == nil {
		t.Error("expected refusal for the base/main checkout")
	}
}
