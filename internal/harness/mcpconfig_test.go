package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// readMCP decodes <dir>/.mcp.json into a generic map for assertions.
func readMCP(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	root := map[string]any{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return root
}

func servers(t *testing.T, dir string) map[string]any {
	t.Helper()
	root := readMCP(t, dir)
	s, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("no mcpServers object: %v", root)
	}
	return s
}

// TestClaudeMCPSetCreates: Set on a fresh dir creates the entry.
func TestClaudeMCPSetCreates(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)
	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/h/bin/botfam", Args: []string{"serve"}, Scope: Project}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := c.Get("botfam", Project)
	if err != nil || !ok {
		t.Fatalf("Get botfam: ok=%v err=%v", ok, err)
	}
	if got.Command != "/h/bin/botfam" || len(got.Args) != 1 || got.Args[0] != "serve" {
		t.Errorf("got %+v", got)
	}
}

// TestClaudeMCPSetPreservesForeign is the #227 regression test: Set must not
// clobber a hand-added foreign server.
func TestClaudeMCPSetPreservesForeign(t *testing.T) {
	wt := t.TempDir()
	// Pre-seed a foreign server plus an unknown top-level key.
	seed := `{
  "unknownTopLevel": {"keep": true},
  "mcpServers": {
    "codebase-memory-mcp": {"command": "/usr/local/bin/memory", "args": ["--port", "7777"]}
  }
}`
	if err := os.WriteFile(filepath.Join(wt, ".mcp.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewClaudeMCPConfigurator(wt)
	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/h/bin/botfam", Args: []string{"serve"}, Scope: Project}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	s := servers(t, wt)
	if _, ok := s["codebase-memory-mcp"]; !ok {
		t.Fatalf("foreign server codebase-memory-mcp was clobbered: %v", s)
	}
	if _, ok := s["botfam"]; !ok {
		t.Fatalf("botfam not added: %v", s)
	}
	root := readMCP(t, wt)
	if _, ok := root["unknownTopLevel"]; !ok {
		t.Errorf("unknown top-level key dropped: %v", root)
	}
	// Foreign entry contents unchanged.
	foreign := s["codebase-memory-mcp"].(map[string]any)
	if foreign["command"] != "/usr/local/bin/memory" {
		t.Errorf("foreign command mutated: %v", foreign)
	}
}

// TestClaudeMCPSetIdempotent: two Sets of the same spec are byte-identical.
func TestClaudeMCPSetIdempotent(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)
	spec := MCPServerSpec{
		Name:    "forge",
		Command: "/h/bin/gitea-mcp-server",
		Args:    []string{"-t", "stdio", "-H", "http://x/"},
		Env:     map[string]string{"GITEA_ACCESS_TOKEN_FILE": "/t"},
		Scope:   Project,
	}
	if err := c.Set(spec); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(wt, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set(spec); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(wt, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("non-idempotent Set:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestClaudeMCPRemove: no-op when absent, removes when present, preserves others.
func TestClaudeMCPRemove(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)

	// Remove on a non-existent file / absent server is a no-op (no error).
	if err := c.Remove("ghost", Project); err != nil {
		t.Fatalf("Remove absent (no file): %v", err)
	}

	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/b", Scope: Project}); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(MCPServerSpec{Name: "keep", Command: "/k", Scope: Project}); err != nil {
		t.Fatal(err)
	}
	// Absent server is still a no-op and leaves both entries.
	if err := c.Remove("ghost", Project); err != nil {
		t.Fatalf("Remove absent: %v", err)
	}
	if names, _ := c.List(Project); len(names) != 2 {
		t.Fatalf("expected 2 servers after no-op remove, got %v", names)
	}
	// Present server is removed; sibling preserved.
	if err := c.Remove("botfam", Project); err != nil {
		t.Fatalf("Remove present: %v", err)
	}
	if _, ok, _ := c.Get("botfam", Project); ok {
		t.Error("botfam still present after Remove")
	}
	if _, ok, _ := c.Get("keep", Project); !ok {
		t.Error("Remove deleted unrelated sibling 'keep'")
	}
}

// TestClaudeMCPListAndGlobal exercises List ordering and the Global stub error.
func TestClaudeMCPListAndGlobal(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := c.Set(MCPServerSpec{Name: n, Command: "/" + n, Scope: Project}); err != nil {
			t.Fatal(err)
		}
	}
	names, err := c.List(Project)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("List not sorted: %v", names)
		}
	}
	if _, err := c.List(Global); err == nil {
		t.Error("expected Global scope to be unimplemented")
	}
	if c.Harness() != "claude-code" {
		t.Errorf("Harness() = %q", c.Harness())
	}
}

// TestRenderClaudeMCPPreservesForeign: the #227 regression at the renderer
// level — RenderClaudeMCP yields botfam(+gopls) AND keeps a foreign entry.
func TestRenderClaudeMCPPreservesForeign(t *testing.T) {
	wt := t.TempDir()
	seed := `{
  "mcpServers": {
    "codebase-memory-mcp": {"command": "/usr/local/bin/memory"}
  }
}`
	if err := os.WriteFile(filepath.Join(wt, ".mcp.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderClaudeMCP(wt); err != nil {
		t.Fatalf("RenderClaudeMCP: %v", err)
	}
	s := servers(t, wt)
	for _, want := range []string{"botfam", "codebase-memory-mcp"} {
		if _, ok := s[want]; !ok {
			t.Errorf("missing server %q after render: %v", want, s)
		}
	}
	if lookGopls() != "" {
		if _, ok := s["gopls"]; !ok {
			t.Errorf("gopls installed but not rendered: %v", s)
		}
	}
}

func TestAntigravityMCPConfigurator(t *testing.T) {
	tmp := t.TempDir()
	geminiDir := filepath.Join(tmp, ".gemini", "antigravity")
	if err := os.MkdirAll(geminiDir, 0755); err != nil {
		t.Fatal(err)
	}

	c := &AntigravityMCPConfigurator{HomeDir: tmp}
	if c.Harness() != "antigravity" {
		t.Errorf("Harness() = %q", c.Harness())
	}

	// Project scope returns error
	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/b", Scope: Project}); err == nil {
		t.Error("expected Project scope to be unimplemented")
	}

	// Global scope Set works
	spec := MCPServerSpec{
		Name:    "botfam",
		Command: "/usr/bin/botfam",
		Args:    []string{"serve"},
		Env:     map[string]string{"GITEA_ACCESS_TOKEN_FILE": "/t"},
		Scope:   Global,
	}
	if err := c.Set(spec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get works
	got, ok, err := c.Get("botfam", Global)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Command != "/usr/bin/botfam" || got.Args[0] != "serve" || got.Env["GITEA_ACCESS_TOKEN_FILE"] != "/t" {
		t.Errorf("got spec: %+v", got)
	}

	// List works
	names, err := c.List(Global)
	if err != nil || len(names) != 1 || names[0] != "botfam" {
		t.Errorf("List: got %v, err=%v", names, err)
	}

	// Remove works
	if err := c.Remove("botfam", Global); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	names, _ = c.List(Global)
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestCodexMCPConfigurator(t *testing.T) {
	tmp := t.TempDir()
	codexDir := filepath.Join(tmp, ".codex")
	if err := os.MkdirAll(codexDir, 0755); err != nil {
		t.Fatal(err)
	}

	c := &CodexMCPConfigurator{HomeDir: tmp}
	if c.Harness() != "codex" {
		t.Errorf("Harness() = %q", c.Harness())
	}

	// Project scope returns error
	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/b", Scope: Project}); err == nil {
		t.Error("expected Project scope to be unimplemented")
	}

	// Global scope Set works
	spec := MCPServerSpec{
		Name:    "botfam",
		Command: "/usr/bin/botfam",
		Args:    []string{"serve"},
		Env:     map[string]string{"GITEA_ACCESS_TOKEN_FILE": "/t"},
		Scope:   Global,
	}
	if err := c.Set(spec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get works
	got, ok, err := c.Get("botfam", Global)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Command != "/usr/bin/botfam" || got.Args[0] != "serve" || got.Env["GITEA_ACCESS_TOKEN_FILE"] != "/t" {
		t.Errorf("got spec: %+v", got)
	}

	// List works
	names, err := c.List(Global)
	if err != nil || len(names) != 1 || names[0] != "botfam" {
		t.Errorf("List: got %v, err=%v", names, err)
	}

	// Remove works
	if err := c.Remove("botfam", Global); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	names, _ = c.List(Global)
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

// TestClaudeMCPSetConcurrent is the executable Detection for #272: N concurrent
// Set calls each adding a distinct server must ALL survive. Without the
// flock-serialized read-modify-write, the unprotected loadRaw→merge→writeRaw
// cycle loses updates (last writer wins), and fewer than N servers remain.
func TestClaudeMCPSetConcurrent(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)
	const n = 24

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- c.Set(MCPServerSpec{
				Name:    fmt.Sprintf("srv-%02d", i),
				Command: "/bin/echo",
				Args:    []string{fmt.Sprintf("%d", i)},
				Scope:   Project,
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Set: %v", err)
		}
	}

	got := servers(t, wt)
	if len(got) != n {
		t.Fatalf("lost-update clobber: want %d servers to survive, got %d", n, len(got))
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("srv-%02d", i)
		if _, ok := got[name]; !ok {
			t.Errorf("server %q was dropped by a concurrent writer", name)
		}
	}
}

// TestClaudeMCPSetAtomicWriteNoTornFile asserts a successful Set leaves a
// well-formed, fully-parseable file (the tmp+rename half of #272) and no leftover
// temp debris that would corrupt a later parse. Only .mcp.json and its sibling
// lock file may remain.
func TestClaudeMCPSetAtomicWriteNoTornFile(t *testing.T) {
	wt := t.TempDir()
	c := NewClaudeMCPConfigurator(wt)
	if err := c.Set(MCPServerSpec{Name: "botfam", Command: "/h/bin/botfam", Args: []string{"serve"}, Scope: Project}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// readMCP fails the test if .mcp.json is missing or not valid JSON (torn).
	readMCP(t, wt)

	entries, err := os.ReadDir(wt)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if name := e.Name(); name != ".mcp.json" && name != ".mcp.json.lock" {
			t.Errorf("unexpected leftover file in worktree: %q (atomicWrite temp not cleaned up?)", name)
		}
	}
}
