package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildDiscoveryDataPrefersRegistryName verifies the human fam name from
// fam.toml wins over the resolver's root-set id (regression: the root resource
// was showing "fam-<hash>" instead of the configured name).
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
