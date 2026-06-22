package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/robertolupi/botfam/internal/docs"
	"github.com/robertolupi/botfam/internal/famconfig"
)

func TestFileURIToPath(t *testing.T) {
	cases := map[string]string{
		"file:///Users/x/wt-agy": "/Users/x/wt-agy",
		"file://host/abs/path":   "/abs/path",
		"https://example/x":      "",
		"/not/a/uri":             "",
	}
	for in, want := range cases {
		if got := fileURIToPath(in); got != want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestOrientToolReturnsDiscoveryRoot verifies the orient tool returns the
// botfam.discovery.v1 index for the given work_dir, bypassing the membership
// preamble (#132).
func TestOrientToolReturnsDiscoveryRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{"myfam": {Path: root}},
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{}
	res, err := s.callTool(context.Background(), "orient", map[string]any{"work_dir": root})
	if err != nil {
		t.Fatalf("orient: %v", err)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "botfam.discovery.v1") {
		t.Errorf("orient output missing discovery schema: %q", text)
	}
}

// resolves builds a fam-detection predicate that approves exactly the given
// dirs, standing in for the env-coupled famResolvable in resolveWorkDir tests.
func resolves(dirs ...string) func(string) bool {
	set := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		set[d] = true
	}
	return func(p string) bool { return set[p] }
}

// TestResolveWorkDirRootsTier exercises the client `roots` tier — the path that
// is dead code on per-project mounts (cwd!="/") and was therefore unvalidated by
// real Claude harness boots (#136). On a system-wide mount (cwd=="/") with no
// ambient fam-root override, a fam-resolvable client root must win and label as "roots".
func TestResolveWorkDirRootsTier(t *testing.T) {
	root := "/Users/x/wt-claude"
	requestRoots := func(ctx context.Context) (*mcplib.ListRootsResult, error) {
		return &mcplib.ListRootsResult{Roots: []mcplib.Root{{URI: "file://" + root}}}, nil
	}
	dir, via := resolveWorkDir(context.Background(), "/", "", requestRoots, resolves(root))
	if dir != root || via != "roots" {
		t.Errorf("resolveWorkDir = (%q, %q), want (%q, roots)", dir, via, root)
	}
}

// TestResolveWorkDirSkipsUnresolvableRoots verifies the roots tier ignores a
// client root that is not fam-resolvable and keeps scanning (#136).
func TestResolveWorkDirSkipsUnresolvableRoots(t *testing.T) {
	good := "/Users/x/wt-claude"
	requestRoots := func(ctx context.Context) (*mcplib.ListRootsResult, error) {
		return &mcplib.ListRootsResult{Roots: []mcplib.Root{
			{URI: "file:///tmp/not-a-fam"},
			{URI: "file://" + good},
		}}, nil
	}
	dir, via := resolveWorkDir(context.Background(), "/", "", requestRoots, resolves(good))
	if dir != good || via != "roots" {
		t.Errorf("resolveWorkDir = (%q, %q), want (%q, roots)", dir, via, good)
	}
}

// TestResolveWorkDirRootsPrioritizedOverCWD asserts that client roots are
// prioritized over CWD, so if both are present and resolvable, the roots tier wins.
func TestResolveWorkDirRootsPrioritizedOverCWD(t *testing.T) {
	project := "/Users/x/wt-claude"
	other := "/Users/x/wt-other"
	called := false
	requestRoots := func(ctx context.Context) (*mcplib.ListRootsResult, error) {
		called = true
		return &mcplib.ListRootsResult{Roots: []mcplib.Root{{URI: "file://" + other}}}, nil
	}
	dir, via := resolveWorkDir(context.Background(), project, "", requestRoots, resolves(project, other))
	if dir != other || via != "roots" {
		t.Errorf("resolveWorkDir = (%q, %q), want (%q, roots)", dir, via, other)
	}
	if !called {
		t.Error("expected client roots to be consulted and win over CWD")
	}
}

// TestResolveWorkDirRootsFallthroughToPWD covers a system-wide mount whose
// client either has no roots capability or returns nothing addressable: it must
// fall through to a fam-resolvable PWD (#136).
func TestResolveWorkDirRootsFallthroughToPWD(t *testing.T) {
	pwd := "/Users/x/wt-claude"

	// No roots capability at all (requestRoots nil).
	if dir, via := resolveWorkDir(context.Background(), "/", pwd, nil, resolves(pwd)); dir != pwd || via != "pwd" {
		t.Errorf("no-roots: resolveWorkDir = (%q, %q), want (%q, pwd)", dir, via, pwd)
	}

	// Roots present but none fam-resolvable: fall through to PWD.
	empty := func(ctx context.Context) (*mcplib.ListRootsResult, error) {
		return &mcplib.ListRootsResult{Roots: []mcplib.Root{{URI: "file:///tmp/not-a-fam"}}}, nil
	}
	if dir, via := resolveWorkDir(context.Background(), "/", pwd, empty, resolves(pwd)); dir != pwd || via != "pwd" {
		t.Errorf("unresolvable-roots: resolveWorkDir = (%q, %q), want (%q, pwd)", dir, via, pwd)
	}
}

// TestRenderIndexJSONIncludesResolvedVia verifies resolved_via is surfaced on
// the structured index (#137).
func TestRenderIndexJSONIncludesResolvedVia(t *testing.T) {
	d := discoveryData{resolvedVia: "cwd"}
	body, err := renderIndexJSON(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\"resolved_via\": \"cwd\"") {
		t.Errorf("index.json missing resolved_via: %s", body)
	}
}

// TestBuildDiscoveryDataPrefersRegistryName verifies the human fam name from
// fam.toml wins over the resolver's root-set id (#130).
func TestBuildDiscoveryDataPrefersRegistryName(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "wt-agy")
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{"myfam": {Path: root}},
	}); err != nil {
		t.Fatal(err)
	}
	d := buildDiscoveryData(context.Background(), wt, "")
	if d.tmpl.Fam != "myfam" {
		t.Errorf("Fam = %q, want %q (registry name must win over the resolver id)", d.tmpl.Fam, "myfam")
	}
}

func TestForgeTokenHealthCheck(t *testing.T) {
	workDir := t.TempDir()
	actor := "testactor"

	findCheck := func(checks []healthCheck, name string) *healthCheck {
		for i := range checks {
			if checks[i].Check == name {
				return &checks[i]
			}
		}
		return nil
	}

	// Case 1: actor resolved, harness unknown → forge_token warn (not silently absent)
	checks := discoveryHealth(workDir, docs.TemplateData{Actor: actor}, "", "")
	hc := findCheck(checks, "forge_token")
	if hc == nil {
		t.Fatal("forge_token check missing when harness is empty")
	}
	if hc.Status != "warn" {
		t.Errorf("expected warn when harness empty, got %q", hc.Status)
	}

	// Case 2: actor + harness known, token file absent → warn with path
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	checks = discoveryHealth(workDir, docs.TemplateData{Actor: actor}, "claude-code", "")
	hc = findCheck(checks, "forge_token")
	if hc == nil {
		t.Fatal("forge_token check missing when token file absent")
	}
	if hc.Status != "warn" {
		t.Errorf("expected warn when token absent, got %q", hc.Status)
	}

	// Case 3: token file present → ok
	tokenDir := filepath.Join(tmpHome, ".botfam")
	if err := os.MkdirAll(tokenDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tokenDir, "token-claude-code"), []byte("mytoken"), 0600); err != nil {
		t.Fatal(err)
	}
	checks = discoveryHealth(workDir, docs.TemplateData{Actor: actor}, "claude-code", "")
	hc = findCheck(checks, "forge_token")
	if hc == nil {
		t.Fatal("forge_token check missing when token present")
	}
	if hc.Status != "ok" {
		t.Errorf("expected ok when token present, got %q", hc.Status)
	}
}
