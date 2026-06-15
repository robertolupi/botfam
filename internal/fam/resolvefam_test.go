package fam

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
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

// TestForgeIdentityParity is the #231/#252 acceptance test: the three
// independent consumers of fam identity resolve IDENTICAL forge_url / owner/repo
// / token / actor for one worktree, proving the "one resolver" goal (no #183
// divergence). The consumers exercised:
//
//   - `botfam credential`     → famconfig.ResolveFam (via fam.ResolveFam)
//   - the forge MCP client    → forge.NewClient
//   - whoami / orient         → fam.Resolver.Resolve
//
// All env short-circuits (GITEA_*, COLLAB_ROOT) are cleared so every path is
// forced through fam.toml — if any consumer re-derived identity its own way,
// the asserted values would drift apart.
func TestForgeIdentityParity(t *testing.T) {
	famDir := resolveFamFixture(t)
	wt := filepath.Join(famDir, "claude") // [agent.claude], harness claude-code
	gitInit(t, wt)

	// The per-harness token on disk that all three must agree to use.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".botfam"), 0o755); err != nil {
		t.Fatal(err)
	}
	const tokenVal = "parity-token-xyz"
	if err := os.WriteFile(filepath.Join(home, ".botfam", "token-claude-code"), []byte(tokenVal+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	for _, k := range []string{"GITEA_URL", "GITEA_OWNER", "GITEA_REPO", "GITEA_TOKEN", "COLLAB_ROOT", "BOTFAM_FORGE_REMOTE"} {
		t.Setenv(k, "")
	}

	// 1. botfam credential.
	rf, err := ResolveFam(wt)
	if err != nil {
		t.Fatalf("ResolveFam: %v", err)
	}
	// 2. forge MCP client.
	cl, err := forge.NewClient(wt, rf.Actor)
	if err != nil {
		t.Fatalf("forge.NewClient: %v", err)
	}
	// 3. whoami / orient.
	info, err := (Resolver{WorkDir: wt}).Resolve()
	if err != nil {
		t.Fatalf("Resolver.Resolve: %v", err)
	}

	// forge_url parity (NewClient guarantees a trailing slash).
	wantBase := rf.ForgeURL
	if !strings.HasSuffix(wantBase, "/") {
		wantBase += "/"
	}
	if cl.BaseURL != wantBase {
		t.Errorf("forge_url diverges: NewClient=%q ResolveFam=%q", cl.BaseURL, wantBase)
	}

	// owner/repo parity (fam.toml repository is "owner/repo").
	parts := strings.SplitN(rf.Repository, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("repository %q is not owner/repo", rf.Repository)
	}
	if cl.Owner != parts[0] || cl.Repo != parts[1] {
		t.Errorf("owner/repo diverges: NewClient=%q/%q fam.toml=%q/%q", cl.Owner, cl.Repo, parts[0], parts[1])
	}

	// token parity: NewClient's token == contents of the file ResolveFam points at.
	rawTok, err := os.ReadFile(rf.TokenPath)
	if err != nil {
		t.Fatalf("read token at ResolveFam.TokenPath=%q: %v", rf.TokenPath, err)
	}
	if got := strings.TrimSpace(string(rawTok)); cl.Token != got || cl.Token != tokenVal {
		t.Errorf("token diverges: NewClient=%q tokenfile(%s)=%q want=%q", cl.Token, rf.TokenPath, got, tokenVal)
	}

	// actor / fam name / root parity across credential and whoami.
	if rf.Actor != info.Actor {
		t.Errorf("actor diverges: ResolveFam=%q Resolver=%q", rf.Actor, info.Actor)
	}
	if rf.Name != info.Name {
		t.Errorf("fam name diverges: ResolveFam=%q Resolver=%q", rf.Name, info.Name)
	}
	if info.Root != rf.FamDir {
		t.Errorf("root/famDir diverges: Resolver.Root=%q ResolveFam.FamDir=%q", info.Root, rf.FamDir)
	}
}

// TestResolverLegacyNoFamTOML guards the non-agent escape hatch #252 must keep
// intact: a repo with NO fam.toml still resolves a (hashed) fam root via git
// history rather than erroring.
func TestResolverLegacyNoFamTOML(t *testing.T) {
	famDir := t.TempDir() // deliberately no fam.toml
	wt := filepath.Join(famDir, "legacy")
	gitInit(t, wt)
	for _, k := range []string{"COLLAB_ROOT", "BOTFAM_FAM"} {
		t.Setenv(k, "")
	}

	info, err := (Resolver{WorkDir: wt}).Resolve()
	if err != nil {
		t.Fatalf("Resolve with no fam.toml should fall back, got: %v", err)
	}
	if info.Actor != "" {
		t.Errorf("actor = %q, want empty (no fam.toml roster)", info.Actor)
	}
	if !strings.HasPrefix(info.Name, "fam-") {
		t.Errorf("name = %q, want hashed fam-<id> fallback", info.Name)
	}
}

// TestWaitIngestEnabled covers the #254 default-on ingester gate driven by the
// `wait_ingest` fam.toml flag (wiki/ProposalFlagFlips): on by fam default, an
// agent override turns it off, and a non-agent/legacy worktree stays on.
func TestWaitIngestEnabled(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	const famTOML = `name       = "deep-cuts"
slug       = "dc"
forge_url  = "http://gitea:3000/"
repository = "deep-cuts/deep-cuts"
roster     = ["claude", "agy"]

[flags]
wait_ingest = 1

[agent.claude]
harness    = "claude-code"
forge_user = "claude-bot"

[agent.agy]
harness    = "antigravity"
forge_user = "agy-bot"
[agent.agy.flags]
wait_ingest = 0
`
	if err := os.WriteFile(filepath.Join(famDir, "fam.toml"), []byte(famTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(famDir, "claude")
	gitInit(t, claude)
	agy := filepath.Join(famDir, "agy")
	gitInit(t, agy)

	on := func(workDir string) bool {
		t.Helper()
		v, err := WaitIngestEnabled(workDir)
		if err != nil {
			t.Fatalf("WaitIngestEnabled(%s): unexpected error %v", workDir, err)
		}
		return v
	}
	if !on(claude) {
		t.Error("claude: wait_ingest should be on (fam default 1)")
	}
	if on(agy) {
		t.Error("agy: wait_ingest should be off (agent override 0)")
	}

	// A legacy / non-agent worktree (no fam.toml) keeps the default-on behavior.
	legacy := filepath.Join(t.TempDir(), "legacy")
	gitInit(t, legacy)
	if !on(legacy) {
		t.Error("legacy worktree without fam.toml should default on")
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
