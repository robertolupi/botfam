package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/forge"
)

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

func TestRegistryAgentTables(t *testing.T) {
	famDir := t.TempDir()
	cfg := deepCutsConfig(famDir)
	reg := famconfig.BuildRegistry(cfg, "deep-cuts", cfg.Repos["deep-cuts"], famDir)

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
	if !ok || claude.Name != "claude" || claude.Harness != "claude-code" || claude.ForgeUser != "claude-bot" || claude.IsUser {
		t.Errorf("agent.claude = %+v ok=%v", claude, ok)
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

// TestResolveFamFromWiki: ResolveFam resolves the enclosing agent worktree when
// called from inside a nested wiki/ git repo, not the nested repo itself. This is
// the acceptance gate for issue #264: `botfam memory list` must work from wiki/.
func TestResolveFamFromWiki(t *testing.T) {
	famDir := resolveFamFixture(t)
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)

	// A nested git repo simulating the wiki checkout at <worktree>/wiki.
	wikiDir := filepath.Join(wt, "wiki")
	gitInit(t, wikiDir)

	rf, err := ResolveFam(wikiDir)
	if err != nil {
		t.Fatalf("ResolveFam from wiki/: %v", err)
	}
	if rf.Actor != "claude" {
		t.Errorf("actor = %q, want claude", rf.Actor)
	}
	if rf.WorktreeRoot != wt {
		t.Errorf("WorktreeRoot = %q, want %q", rf.WorktreeRoot, wt)
	}
	if rf.Slug != "dc" {
		t.Errorf("slug = %q, want dc", rf.Slug)
	}
}

// TestResolverBareNameActor: the core Resolver (used by whoami and friends)
// resolves a bare-name worktree's actor against the merged roster (the wt-
// prefix is retired); the base/main checkout (not a roster member) gets none.
func TestResolverBareNameActor(t *testing.T) {
	famDir := resolveFamFixture(t)

	agy := filepath.Join(famDir, "agy")
	gitInit(t, agy)
	info, err := (famconfig.GitResolver{}).ResolveIdentity(agy)
	if err != nil {
		t.Fatalf("Resolve(agy): %v", err)
	}
	if info.Actor != "agy" {
		t.Errorf("actor = %q, want agy (bare-name from roster)", info.Actor)
	}

	main := filepath.Join(famDir, "main")
	gitInit(t, main)
	info2, err := (famconfig.GitResolver{}).ResolveIdentity(main)
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
// divergence). All env short-circuits (GITEA_*) are cleared so every path is
// forced through ~/.botfam/config.toml.
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
	for _, k := range []string{"GITEA_URL", "GITEA_OWNER", "GITEA_REPO", "GITEA_TOKEN", "BOTFAM_FORGE_REMOTE"} {
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
	info, err := (famconfig.GitResolver{}).ResolveIdentity(wt)
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

	// owner/repo parity (config repository is "owner/repo").
	parts := strings.SplitN(rf.Repository, "/", 2)
	if len(parts) != 2 {
		t.Fatalf("repository %q is not owner/repo", rf.Repository)
	}
	if cl.Owner != parts[0] || cl.Repo != parts[1] {
		t.Errorf("owner/repo diverges: NewClient=%q/%q config=%q/%q", cl.Owner, cl.Repo, parts[0], parts[1])
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
	if info.FamDir != rf.FamDir {
		t.Errorf("root/famDir diverges: Resolver.FamDir=%q ResolveFam.FamDir=%q", info.FamDir, rf.FamDir)
	}
}

// TestResolverNoConfig: a repo with NO matching [repo.<k>] stanza still resolves
// a fam dir (parent of the git toplevel) and an actor without erroring — the
// permissive path used by whoami/doctor; only the strict ResolveFam fails loud.
func TestResolverNoConfig(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	// Isolated, nonexistent config: no stanza matches.
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	wt := filepath.Join(famDir, "legacy")
	gitInit(t, wt)

	info, err := (famconfig.GitResolver{}).ResolveIdentity(wt)
	if err != nil {
		t.Fatalf("Resolve with no config should not error, got: %v", err)
	}
	if info.Actor != "" {
		t.Errorf("actor = %q, want empty (no roster)", info.Actor)
	}
	if info.FamDir != famDir {
		t.Errorf("famDir = %q, want %q", info.FamDir, famDir)
	}
	if info.Name != filepath.Base(famDir) {
		t.Errorf("name = %q, want fam-dir basename %q", info.Name, filepath.Base(famDir))
	}
}

func TestResolveFamNoConfig(t *testing.T) {
	famDir := t.TempDir()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml")) // nonexistent
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)

	if _, err := ResolveFam(wt); err == nil {
		t.Fatal("expected loud error when no config stanza matches, got nil")
	}
}
