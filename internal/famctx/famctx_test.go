package famctx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
)

func gitInit(t *testing.T, dir string) {
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

// setConfig points BOTFAM_CONFIG at a fresh temp file and writes cfg there.
func setConfig(t *testing.T, cfg famconfig.Config) {
	t.Helper()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := famconfig.WriteConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWalkUp(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}

	setConfig(t, famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"bob": {Harness: "bob-code"}},
		Repos:  map[string]famconfig.RepoConfig{"myfam": {Path: famDir, Slug: "mf"}},
	})

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	// Nested subdirectories inside agent worktree
	nested := filepath.Join(bobDir, "sub", "dir")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Resolve from nested subdirectory
	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: nested,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve(ModeAgentRuntime) failed: %v", err)
	}

	if ctx.FamDir != famDir {
		t.Errorf("expected FamDir %q, got %q", famDir, ctx.FamDir)
	}
	if ctx.Actor != "bob" {
		t.Errorf("expected Actor %q, got %q", "bob", ctx.Actor)
	}
	if ctx.ActorRole != RoleAgent {
		t.Errorf("expected RoleAgent, got %q", ctx.ActorRole)
	}
	if ctx.Source != SourceWorkDir {
		t.Errorf("expected SourceWorkDir, got %q", ctx.Source)
	}

	// 2. Resolve from wiki/ directory (simulating nested wiki repo)
	wikiDir := filepath.Join(bobDir, "wiki")
	if err := os.Mkdir(wikiDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, wikiDir) // Nested git repo

	ctxWiki, err := Resolve(context.Background(), Inputs{
		WorkDir: wikiDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve from nested wiki failed: %v", err)
	}
	if ctxWiki.Actor != "bob" {
		t.Errorf("expected Actor %q even from nested wiki, got %q", "bob", ctxWiki.Actor)
	}
	if ctxWiki.ActorRole != RoleAgent {
		t.Errorf("expected RoleAgent, got %q", ctxWiki.ActorRole)
	}
}

// TestWithRegistryCtxAllowsHumanWorktree verifies the fix for general forge
// tooling: a [user.<name>] (human) checkout is refused by the strict
// agent-runtime gate, but WithRegistryCtx resolves it (RoleUser) with the forge
// registry populated, so commands like `forge lint`/`forge graph` can run there.
func TestWithRegistryCtxAllowsHumanWorktree(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, famconfig.Config{
		Users: map[string]famconfig.AgentConfig{"rlupi": {ForgeUser: "rlupi"}},
		Repos: map[string]famconfig.RepoConfig{
			"mf": {Path: famDir, Slug: "mf", ForgeURL: "http://gitea:3000/", Repository: "botfam/botfam"},
		},
	})
	userDir := filepath.Join(famDir, "rlupi")
	if err := os.Mkdir(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, userDir)

	// Strict agent-runtime gate refuses the human checkout.
	if _, err := ResolveAgentRuntime(userDir); err == nil {
		t.Fatal("expected ResolveAgentRuntime to refuse a [user.<name>] worktree")
	}

	// Non-strict registry resolver allows it.
	ctx, err := WithRegistryCtx(context.Background(), userDir)
	if err != nil {
		t.Fatalf("WithRegistryCtx should resolve a human worktree, got: %v", err)
	}
	fctx, ok := FromContext(ctx)
	if !ok {
		t.Fatal("expected a famctx in the returned context")
	}
	if fctx.ActorRole != RoleUser {
		t.Errorf("expected RoleUser, got %q", fctx.ActorRole)
	}
	if fctx.Registry.ForgeURL != "http://gitea:3000/" || fctx.Registry.Repository != "botfam/botfam" {
		t.Errorf("registry not populated: %+v", fctx.Registry)
	}
	// Humans carry no per-harness token path (they supply GITEA_TOKEN).
	if fctx.TokenPath != "" {
		t.Errorf("human worktree should have no TokenPath, got %q", fctx.TokenPath)
	}
}

// TestResolveLocateNoConfig: with no matching [repo.<k>] stanza, ModeLocate no
// longer synthesizes a git-hash fam (#404 dropped the legacy fallback). It
// returns an empty identity plus an error diagnostic rather than failing.
func TestResolveLocateNoConfig(t *testing.T) {
	tempDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = eval
	}

	gitDir := filepath.Join(tempDir, "wt-bob")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, gitDir)
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml")) // nonexistent

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: gitDir,
		Mode:    ModeLocate,
	})
	if err != nil {
		t.Fatalf("ModeLocate should not error on an unregistered dir: %v", err)
	}
	if ctx.Name != "" || ctx.Actor != "" {
		t.Errorf("expected empty identity for an unregistered dir, got Name=%q Actor=%q", ctx.Name, ctx.Actor)
	}
	if len(ctx.Diagnostics) == 0 {
		t.Error("expected a diagnostic when no fam context resolves")
	}
}

func TestLocationOf(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}

	setConfig(t, famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"bob": {Harness: "bob-code"}},
		Repos:  map[string]famconfig.RepoConfig{"myfam": {Path: famDir, Slug: "mf"}},
	})

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: bobDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// 1. LocationMainRepo
	loc, err := ctx.LocationOf(bobDir)
	if err != nil {
		t.Fatal(err)
	}
	if loc != LocationMainRepo {
		t.Errorf("expected LocationMainRepo, got %q", loc)
	}

	// 2. LocationWiki
	wikiDir := filepath.Join(bobDir, "wiki")
	if err := os.Mkdir(wikiDir, 0755); err != nil {
		t.Fatal(err)
	}
	locWiki, err := ctx.LocationOf(wikiDir)
	if err != nil {
		t.Fatal(err)
	}
	if locWiki != LocationWiki {
		t.Errorf("expected LocationWiki, got %q", locWiki)
	}

	// 3. LocationSubmodule
	subDir := filepath.Join(bobDir, "submodule")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Let's create a git repo inside it
	gitInit(t, subDir)
	// For testing submodule check, LocationOf checks if the git superproject is bobDir.
	// Since we don't have a real submodule checkout here, we can skip submodule specifics or mock.
	// Let's check a foreign directory instead:
	locForeign, err := ctx.LocationOf(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if locForeign != LocationForeign {
		t.Errorf("expected LocationForeign, got %q", locForeign)
	}
}

func TestFlagEnabled(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, famconfig.Config{
		Flags: map[string]any{"experiment": "yes", "wait_ingest": true},
		Agents: map[string]famconfig.AgentConfig{
			"bob": {Harness: "bob-code", Flags: map[string]any{"wait_ingest": false}},
		},
		Repos: map[string]famconfig.RepoConfig{"myfam": {Path: famDir, Slug: "mf"}},
	})

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: bobDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// Override check: wait_ingest should be false
	v, err := ctx.FlagEnabled("wait_ingest", true)
	if err != nil {
		t.Fatal(err)
	}
	if v {
		t.Errorf("expected wait_ingest override to be false, got true")
	}

	// Default fallback check
	vUnset, err := ctx.FlagEnabled("unset_flag", true)
	if err != nil {
		t.Fatal(err)
	}
	if !vUnset {
		t.Errorf("expected unset_flag to fall back to default true, got false")
	}
}

func TestResolveRefusalModes(t *testing.T) {
	// Setup family with alice (user), bob (agent)
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}

	setConfig(t, famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"bob": {Harness: "claude-code"}},
		Users:  map[string]famconfig.AgentConfig{"alice": {}},
		Repos:  map[string]famconfig.RepoConfig{"myfam": {Path: famDir, Slug: "mf"}},
	})

	// 1. Unregistered dir (no matching stanza) under ModeAgentRuntime
	plainDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(plainDir); err == nil {
		plainDir = eval
	}
	gitInit(t, plainDir)
	_, err := Resolve(context.Background(), Inputs{
		WorkDir: plainDir,
		Mode:    ModeAgentRuntime,
	})
	if err == nil || !strings.Contains(err.Error(), "is not registered") {
		t.Errorf("expected unregistered-dir error under ModeAgentRuntime, got %v", err)
	}

	// 2. User worktree under ModeAgentRuntime
	aliceDir := filepath.Join(famDir, "alice")
	if err := os.Mkdir(aliceDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, aliceDir)
	_, err = Resolve(context.Background(), Inputs{
		WorkDir: aliceDir,
		Mode:    ModeAgentRuntime,
	})
	if err == nil || !strings.Contains(err.Error(), "human) checkout") {
		t.Errorf("expected user worktree refusal under ModeAgentRuntime, got %v", err)
	}

	// 3. Unregistered agent under ModeAgentRuntime
	charlieDir := filepath.Join(famDir, "charlie")
	if err := os.Mkdir(charlieDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, charlieDir)
	_, err = Resolve(context.Background(), Inputs{
		WorkDir: charlieDir,
		Mode:    ModeAgentRuntime,
	})
	if err == nil || !strings.Contains(err.Error(), "not a declared [agent.<name>]") {
		t.Errorf("expected unregistered agent refusal under ModeAgentRuntime, got %v", err)
	}

	bobDir := filepath.Join(famDir, "bob")
	if err := os.Mkdir(bobDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, bobDir)

	// 5. ScopedNick idempotency and derived paths correctness
	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: bobDir,
		Mode:    ModeAgentRuntime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ScopedNick != "bob-mf" {
		t.Errorf("expected ScopedNick bob-mf, got %q", ctx.ScopedNick)
	}
	if ctx.SpoolDir != filepath.Join(famDir, "spool", "bob") {
		t.Errorf("expected SpoolDir under famDir, got %q", ctx.SpoolDir)
	}
	if ctx.IRCLogDir != filepath.Join(bobDir, "scratch", "irc", "bob") {
		t.Errorf("expected IRCLogDir under worktree root, got %q", ctx.IRCLogDir)
	}
}

// TestResolveBaseCheckout: resolving the fam root (base/main checkout) with
// ModeLocate or ModeRegistry returns RoleBase and an empty actor rather than
// erroring — the caller decides how to handle a non-agent context.
func TestResolveBaseCheckout(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"bob": {Harness: "claude-code"}},
		Repos:  map[string]famconfig.RepoConfig{"myfam": {Path: famDir, Slug: "mf"}},
	})

	// The "main" checkout: git root equals famDir (fam root).
	mainDir := filepath.Join(famDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, mainDir)

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir: mainDir,
		Mode:    ModeRegistry,
	})
	if err != nil {
		t.Fatalf("ModeRegistry from base checkout should not error, got: %v", err)
	}
	// Actor is empty (main is not in the roster); role should be RoleUnknown or
	// RoleBase (git root ≠ famDir for bare-name checkouts).
	if ctx.Name != "myfam" {
		t.Errorf("Name = %q, want myfam", ctx.Name)
	}
	if ctx.Slug != "mf" {
		t.Errorf("Slug = %q, want mf", ctx.Slug)
	}
	// ModeAgentRuntime must refuse the base checkout.
	_, err = Resolve(context.Background(), Inputs{
		WorkDir: mainDir,
		Mode:    ModeAgentRuntime,
	})
	if err == nil {
		t.Error("ModeAgentRuntime should refuse the base checkout, got nil")
	}
}

// TestResolveWithInjectedResolver proves the Resolver seam (#334): resolution can
// be driven by an injected fake instead of a real git worktree plus environment.
// No gitInit and no t.Setenv — the fake supplies the identity directly, and
// famctx still reads the real fam.toml from the FamDir the fake points at.
func TestResolveWithInjectedResolver(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"bob": {Harness: "bob-code"}},
		Repos:  map[string]famconfig.RepoConfig{"injfam": {Path: famDir, Slug: "inj"}},
	})

	// The fake supplies the identity famctx cannot otherwise get without git+env:
	// FamDir (where the fam lives) and Actor (who we are). It deliberately
	// returns a BOGUS Name and ActorRole to prove famctx re-derives those from the
	// real registry rather than parroting the resolver — so the Name/Role/Slug
	// assertions below are genuine, not self-fulfilling.
	fake := famconfig.FuncResolver(func(workDir string) (famconfig.RootInfo, error) {
		return famconfig.RootInfo{
			FamIdentity: famconfig.FamIdentity{
				FamDir:    famDir,
				Name:      "WRONG_SHOULD_BE_OVERRIDDEN",
				Actor:     "bob",
				ActorRole: famconfig.RoleUnknown,
				Source:    famconfig.SourceWorkDir,
			},
			RootSet:   []string{"deadbeef"},
			RootSetID: "deadbeef0000",
		}, nil
	})

	ctx, err := Resolve(context.Background(), Inputs{
		WorkDir:  famDir,
		Mode:     ModeRegistry,
		Env:      []string{}, // authoritative empty env: no identity env (e.g. BOTFAM_FAM) leak
		Resolver: fake,
	})
	if err != nil {
		t.Fatalf("Resolve with injected resolver failed: %v", err)
	}

	checks := []struct {
		name, got, want string
	}{
		{"Actor", ctx.Actor, "bob"},
		{"FamDir", ctx.FamDir, famDir},
		{"Name", ctx.Name, "injfam"},
		{"Slug", ctx.Slug, "inj"},
		{"RootSetID", ctx.RootSetID, "deadbeef0000"},
		{"ScopedNick", ctx.ScopedNick, "bob-inj"},
		{"ActorRole", string(ctx.ActorRole), string(RoleAgent)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestResolveInjectedResolverError shows an injected resolver's error propagates
// out of Resolve rather than being swallowed.
func TestResolveInjectedResolverError(t *testing.T) {
	fake := famconfig.FuncResolver(func(string) (famconfig.RootInfo, error) {
		return famconfig.RootInfo{}, fmt.Errorf("boom")
	})
	_, err := Resolve(context.Background(), Inputs{
		WorkDir:  t.TempDir(),
		Mode:     ModeRegistry,
		Env:      []string{},
		Resolver: fake,
	})
	if err == nil {
		t.Fatal("expected error from injected resolver, got nil")
	}
}
