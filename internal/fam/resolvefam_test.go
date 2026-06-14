package fam

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
roster     = ["claude", "agy", "rlupi"]

[agent.claude]
harness    = "claude-code"
forge_user = "claude-bot"

[agent.agy]
harness    = "antigravity"
forge_user = "agy-bot"
email      = "roberto.lupi+agy@gmail.com"

[user.rlupi]
forge_user = "rlupi"
`

func TestReadRegistryAgentTables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fam.toml")
	if err := os.WriteFile(path, []byte(sampleFamTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	if reg.Name != "deep-cuts" || reg.Slug != "dc" {
		t.Errorf("name/slug = %q/%q, want deep-cuts/dc", reg.Name, reg.Slug)
	}
	if reg.ForgeURL != "http://gitea.home.rlupi.com:3000/" {
		t.Errorf("forge_url = %q", reg.ForgeURL)
	}
	if reg.Repository != "deep-cuts/deep-cuts" {
		t.Errorf("repository = %q", reg.Repository)
	}
	claude, ok := reg.Agents["claude"]
	if !ok {
		t.Fatalf("agent.claude missing; agents=%v", reg.Agents)
	}
	if claude.Name != "claude" || claude.Harness != "claude-code" || claude.ForgeUser != "claude-bot" {
		t.Errorf("agent.claude = %+v", claude)
	}
	if claude.IsUser {
		t.Errorf("agent.claude should not be IsUser")
	}
	if agy := reg.Agents["agy"]; agy.Email != "roberto.lupi+agy@gmail.com" {
		t.Errorf("agent.agy email = %q", agy.Email)
	}
	rlupi, ok := reg.Users["rlupi"]
	if !ok || !rlupi.IsUser || rlupi.Name != "rlupi" {
		t.Errorf("user.rlupi = %+v ok=%v", rlupi, ok)
	}
	if _, isAgent := reg.Agents["rlupi"]; isAgent {
		t.Errorf("rlupi must be a user, not an agent")
	}
}

// gitInit creates a real git repo at dir so ResolveFam's `git rev-parse
// --show-toplevel` resolves to it.
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

// resolveFamFixture writes fam.toml at famDir and git-inits famDir/<worktree>.
func resolveFamFixture(t *testing.T) (famDir string) {
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

func TestResolveFamAgentWorktree(t *testing.T) {
	famDir := resolveFamFixture(t)
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)

	rf, err := ResolveFam(wt)
	if err != nil {
		t.Fatalf("ResolveFam: %v", err)
	}
	if rf.Actor != "claude" {
		t.Errorf("actor = %q, want claude", rf.Actor)
	}
	if rf.Name != "deep-cuts" || rf.Slug != "dc" {
		t.Errorf("name/slug = %q/%q", rf.Name, rf.Slug)
	}
	if rf.FamDir != famDir {
		t.Errorf("famDir = %q, want %q", rf.FamDir, famDir)
	}
	if rf.ForgeURL != "http://gitea.home.rlupi.com:3000/" {
		t.Errorf("forge_url = %q", rf.ForgeURL)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".botfam", "token-claude-code")
	if rf.TokenPath != want {
		t.Errorf("token path = %q, want %q", rf.TokenPath, want)
	}
	if rf.Agent.Harness != "claude-code" {
		t.Errorf("agent harness = %q", rf.Agent.Harness)
	}
}

func TestResolveFamRefusesUserWorktree(t *testing.T) {
	famDir := resolveFamFixture(t)
	wt := filepath.Join(famDir, "rlupi")
	gitInit(t, wt)

	if _, err := ResolveFam(wt); err == nil {
		t.Fatal("expected refusal for a [user.<name>] worktree, got nil")
	}
}

func TestResolveFamRefusesBaseCheckout(t *testing.T) {
	famDir := resolveFamFixture(t)
	// "main" is the base checkout — not a declared agent.
	wt := filepath.Join(famDir, "main")
	gitInit(t, wt)

	if _, err := ResolveFam(wt); err == nil {
		t.Fatal("expected refusal for the base/main checkout, got nil")
	}
}

// TestResolverBareNameActor: the core Resolver (used by whoami and friends)
// resolves a bare-name worktree's actor against the fam.toml roster (the wt-
// prefix is retired); the base/main checkout (not a roster member) gets none.
func TestResolverBareNameActor(t *testing.T) {
	famDir := resolveFamFixture(t)

	agy := filepath.Join(famDir, "agy")
	gitInit(t, agy)
	info, err := (Resolver{WorkDir: agy}).Resolve()
	if err != nil {
		t.Fatalf("Resolve(agy): %v", err)
	}
	if info.Actor != "agy" {
		t.Errorf("actor = %q, want agy (bare-name from roster)", info.Actor)
	}

	main := filepath.Join(famDir, "main")
	gitInit(t, main)
	info2, err := (Resolver{WorkDir: main}).Resolve()
	if err != nil {
		t.Fatalf("Resolve(main): %v", err)
	}
	if info2.Actor != "" {
		t.Errorf("main actor = %q, want empty (not a roster member)", info2.Actor)
	}
}

func TestResolveFamMissingFamTOML(t *testing.T) {
	famDir := t.TempDir() // no fam.toml
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)

	if _, err := ResolveFam(wt); err == nil {
		t.Fatal("expected loud error when fam.toml is absent, got nil")
	}
}
