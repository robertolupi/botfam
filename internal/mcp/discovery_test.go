package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
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

// TestResolveDiscoveryWorkDirPrefersCollabRoot covers tier 1 of the #132
// resolution chain.
func TestResolveDiscoveryWorkDirPrefersCollabRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fam.toml"), []byte("name = \"myfam\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLLAB_ROOT", root)
	s := &server{}
	if got := s.resolveDiscoveryWorkDir(context.Background()); got != root {
		t.Errorf("resolveDiscoveryWorkDir = %q, want COLLAB_ROOT %q", got, root)
	}
}

// TestOrientToolReturnsDiscoveryRoot verifies the orient tool returns the
// botfam.discovery.v1 index for the given work_dir, bypassing the membership
// preamble (#132).
func TestOrientToolReturnsDiscoveryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fam.toml"), []byte("name = \"myfam\"\n"), 0o644); err != nil {
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
