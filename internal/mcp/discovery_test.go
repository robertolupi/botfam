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

// TestResolveDiscoveryWorkDirViaLabelsTier verifies the resolved_via label
// tracks which tier of the resolution chain fired (#137).
func TestResolveDiscoveryWorkDirViaLabelsTier(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COLLAB_ROOT", root)
	s := &server{}
	dir, via := s.resolveDiscoveryWorkDirVia(context.Background())
	if dir != root || via != "collab_root" {
		t.Errorf("resolveDiscoveryWorkDirVia = (%q, %q), want (%q, %q)", dir, via, root, "collab_root")
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
	t.Setenv("COLLAB_ROOT", root)
	t.Setenv("COLLAB_ACTOR", "")
	if err := os.WriteFile(filepath.Join(root, "fam.toml"), []byte("name = \"myfam\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := buildDiscoveryData(root)
	if d.tmpl.Fam != "myfam" {
		t.Errorf("Fam = %q, want %q (registry name must win over the resolver id)", d.tmpl.Fam, "myfam")
	}
}
