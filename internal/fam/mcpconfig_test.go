package fam

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// level — RenderClaudeMCP yields botfam+forge(+gopls) AND keeps a foreign entry.
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
	if err := RenderClaudeMCP(wt, "http://gitea:3000/", "/t/token"); err != nil {
		t.Fatalf("RenderClaudeMCP: %v", err)
	}
	s := servers(t, wt)
	for _, want := range []string{"botfam", "forge", "codebase-memory-mcp"} {
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
