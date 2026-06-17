package famconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

// setConfig points BOTFAM_CONFIG at a fresh temp file and writes cfg there.
func setConfig(t *testing.T, cfg Config) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(dir, "config.toml"))
	if err := WriteConfig(cfg); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
}

// famFixture registers [repo.dc] at a fresh famDir (deep-cuts) in a temp config
// and returns famDir. Callers git-init the worktrees under it.
func famFixture(t *testing.T) (famDir string) {
	t.Helper()
	famDir = t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, Config{
		ForgeURL: "http://gitea.home.rlupi.com:3000/",
		Agents:   map[string]AgentConfig{"claude": {Harness: "claude-code", ForgeUser: "claude-bot"}},
		Users:    map[string]AgentConfig{"rlupi": {ForgeUser: "rlupi"}},
		Repos:    map[string]RepoConfig{"dc": {Path: famDir, Slug: "dc", Repository: "deep-cuts/deep-cuts"}},
	})
	return famDir
}

// flagsConfig mirrors the legacy flags fixture: fam-wide [flags] plus a per-agent
// [agent.agy.flags] override, registered at famDir under [repo.botfam].
func flagsConfig(famDir string) Config {
	return Config{
		ForgeURL: "http://gitea:3000/",
		Flags:    map[string]any{"wait_ingest": int64(1), "experiment": false, "ratio": int64(0)},
		Agents: map[string]AgentConfig{
			"claude": {Harness: "claude-code", ForgeUser: "claude-bot"},
			"agy":    {Harness: "antigravity", ForgeUser: "agy-bot", Flags: map[string]any{"wait_ingest": int64(0), "experiment": "yes"}},
		},
		Repos: map[string]RepoConfig{"botfam": {Path: famDir, Slug: "botfam", Repository: "botfam/botfam"}},
	}
}

func TestLoadConfigBackfillsKeys(t *testing.T) {
	setConfig(t, Config{
		Agents: map[string]AgentConfig{"claude": {Harness: "claude-code"}},
		Users:  map[string]AgentConfig{"rlupi": {ForgeUser: "rlupi"}},
		Repos:  map[string]RepoConfig{"dc": {Path: "/tmp/dc", Slug: "dc"}},
	})
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Agents["claude"].Name != "claude" || cfg.Agents["claude"].Harness != "claude-code" {
		t.Errorf("agent.claude = %+v", cfg.Agents["claude"])
	}
	if u := cfg.Users["rlupi"]; !u.IsUser || u.Name != "rlupi" {
		t.Errorf("user.rlupi = %+v", u)
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
	// The 'claude' alias must resolve to the same canonical token-claude-code so a
	// fam that spells the harness 'claude' shares the real Claude Code token (#371).
	aliased, err := HarnessTokenPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".botfam", "token-claude-code"); aliased != want {
		t.Errorf("HarnessTokenPath(claude) = %q, want %q", aliased, want)
	}
}

func TestCanonicalHarness(t *testing.T) {
	cases := map[string]string{
		"claude":      "claude-code", // #371: alias for the Claude Code harness
		"claude-code": "claude-code", // idempotent
		"codex":       "codex",       // untouched
		"antigravity": "antigravity", // untouched
		"":            "",            // pass-through
	}
	for in, want := range cases {
		if got := CanonicalHarness(in); got != want {
			t.Errorf("CanonicalHarness(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchRepo(t *testing.T) {
	famDir := famFixture(t)
	wt := filepath.Join(famDir, "claude")
	gitInit(t, wt)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// A worktree under the stanza path matches the [repo.dc] stanza.
	if key, _, ok := MatchRepo(cfg, wt); !ok || key != "dc" {
		t.Errorf("MatchRepo(worktree) = %q, %v; want dc, true", key, ok)
	}
	// The fam dir itself matches.
	if key, _, ok := MatchRepo(cfg, famDir); !ok || key != "dc" {
		t.Errorf("MatchRepo(famDir) = %q, %v; want dc, true", key, ok)
	}
	// An unrelated dir does not match.
	if _, _, ok := MatchRepo(cfg, t.TempDir()); ok {
		t.Error("MatchRepo(unrelated) matched; want no match")
	}
}

func TestBuildRegistryMergesFlags(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	cfg := flagsConfig(famDir)
	reg := BuildRegistry(cfg, "botfam", cfg.Repos["botfam"], famDir)

	if reg.Flags["wait_ingest"] != int64(1) {
		t.Errorf("fam-wide wait_ingest = %#v, want int64(1)", reg.Flags["wait_ingest"])
	}
	if reg.Agents["agy"].Flags["experiment"] != "yes" {
		t.Errorf("agent.agy experiment = %#v, want \"yes\"", reg.Agents["agy"].Flags["experiment"])
	}
	if reg.Agents["claude"].Flags != nil {
		t.Errorf("agent.claude has no [flags] table; got %#v", reg.Agents["claude"].Flags)
	}
	if reg.Repository != "botfam/botfam" || reg.ForgeURL != "http://gitea:3000/" || reg.Slug != "botfam" {
		t.Errorf("merged registry = %+v", reg)
	}
}

func TestWriteConfigRoundTripsFlags(t *testing.T) {
	famDir := t.TempDir()
	setConfig(t, flagsConfig(famDir))
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Flags["wait_ingest"] != int64(1) {
		t.Errorf("fam flags lost on rewrite: %#v", cfg.Flags)
	}
	if cfg.Agents["agy"].Flags["wait_ingest"] != int64(0) {
		t.Errorf("agent flags lost on rewrite: %#v", cfg.Agents["agy"].Flags)
	}
}

func TestResolveFlagsAndFlagEnabled(t *testing.T) {
	famDir := t.TempDir()
	cfg := flagsConfig(famDir)
	reg := BuildRegistry(cfg, "botfam", cfg.Repos["botfam"], famDir)

	// on asserts FlagEnabled succeeds and returns the resolved value.
	on := func(actor, name string, def bool) bool {
		t.Helper()
		v, err := FlagEnabled(reg, actor, name, def)
		if err != nil {
			t.Fatalf("FlagEnabled(%s, %s): unexpected error %v", actor, name, err)
		}
		return v
	}

	// claude has no overrides → inherits the fam-wide defaults.
	if !on("claude", "wait_ingest", false) {
		t.Error("claude wait_ingest should be truthy (fam default 1)")
	}
	if on("claude", "experiment", true) {
		t.Error("claude experiment should be false (fam default false)")
	}

	// agy overrides win key-by-key: wait_ingest off, experiment on.
	if on("agy", "wait_ingest", true) {
		t.Error("agy wait_ingest should be false (agent override 0)")
	}
	if !on("agy", "experiment", false) {
		t.Error("agy experiment should be truthy (agent override \"yes\")")
	}

	// Unset flag falls back to the supplied default.
	if !on("claude", "nonexistent", true) {
		t.Error("unset flag should return def=true")
	}

	// Unknown actor → just the fam-wide defaults.
	if !on("ghost", "wait_ingest", false) {
		t.Error("unknown actor should see fam default wait_ingest=1")
	}

	// ResolveFlags merge surface.
	flags := ResolveFlags(reg, "agy")
	if flags["wait_ingest"] != int64(0) || flags["experiment"] != "yes" || flags["ratio"] != int64(0) {
		t.Errorf("ResolveFlags(agy) = %#v", flags)
	}
}

func TestFlagEnabledErrorsOnBadValue(t *testing.T) {
	// A set-but-unparseable value (likely a typo) errors rather than silently
	// reading as off; the returned bool is the caller's default.
	reg := Registry{Flags: map[string]any{"wait_ingest": "treu"}}
	got, err := FlagEnabled(reg, "", "wait_ingest", true)
	if err == nil {
		t.Fatal("expected an error for non-boolean flag value \"treu\"")
	}
	if got != true {
		t.Errorf("on error the default should be returned; got %v want true", got)
	}

	// Every accepted spelling converts without error.
	cases := map[any]bool{
		true: true, false: false,
		int64(1): true, int64(0): false, int64(2): true,
		"on": true, "OFF": false, "Yes": true, "n": false, " true ": true,
	}
	for v, want := range cases {
		reg := Registry{Flags: map[string]any{"f": v}}
		got, err := FlagEnabled(reg, "", "f", !want)
		if err != nil {
			t.Errorf("FlagEnabled(%#v): unexpected error %v", v, err)
			continue
		}
		if got != want {
			t.Errorf("FlagEnabled(%#v) = %v, want %v", v, got, want)
		}
	}
}

func TestResolveFamPopulatesFlags(t *testing.T) {
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	setConfig(t, flagsConfig(famDir))
	wt := filepath.Join(famDir, "agy")
	gitInit(t, wt)

	rf, err := ResolveFam(wt)
	if err != nil {
		t.Fatalf("ResolveFam: %v", err)
	}
	// agy's override turns wait_ingest off; the method uses the effective flags.
	on := func(name string, def bool) bool {
		t.Helper()
		v, err := rf.FlagEnabled(name, def)
		if err != nil {
			t.Fatalf("rf.FlagEnabled(%s): unexpected error %v", name, err)
		}
		return v
	}
	if on("wait_ingest", true) {
		t.Error("rf.FlagEnabled(wait_ingest) should be false for agy (override 0)")
	}
	if !on("experiment", false) {
		t.Error("rf.FlagEnabled(experiment) should be true for agy (override \"yes\")")
	}
	if !on("unset", true) {
		t.Error("rf.FlagEnabled(unset) should fall back to def=true")
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

func TestFamScopedNick(t *testing.T) {
	cases := []struct{ actor, slug, want string }{
		{"claude", "botfam", "claude-botfam"},
		{"agy", "dc", "agy-dc"},
		{"claude-botfam", "botfam", "claude-botfam"}, // idempotent, no double-suffix
		{"claude", "", "claude"},                     // no slug → bare actor
		{"", "botfam", ""},                           // no actor → bare (empty)
	}
	for _, tc := range cases {
		if got := FamScopedNick(tc.actor, tc.slug); got != tc.want {
			t.Errorf("FamScopedNick(%q, %q) = %q, want %q", tc.actor, tc.slug, got, tc.want)
		}
	}
}

func TestFlagFromMap(t *testing.T) {
	flags := map[string]any{"on": int64(1), "off": false, "bad": "treu"}

	if v, err := FlagFromMap(flags, "on", false); err != nil || !v {
		t.Errorf("on => (%v,%v), want (true,nil)", v, err)
	}
	if v, err := FlagFromMap(flags, "off", true); err != nil || v {
		t.Errorf("off => (%v,%v), want (false,nil)", v, err)
	}
	// Absent flag returns the supplied default, no error.
	if v, err := FlagFromMap(flags, "absent", true); err != nil || !v {
		t.Errorf("absent => (%v,%v), want (true,nil)", v, err)
	}
	// Set-but-unparseable value errors and returns the default.
	if v, err := FlagFromMap(flags, "bad", true); err == nil || !v {
		t.Errorf("bad => (%v,%v), want (true, error)", v, err)
	}
}
