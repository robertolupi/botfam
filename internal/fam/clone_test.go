package fam

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestRunCloneRequiresForgeURLBeforeMutating asserts clone fails fast on a
// missing --forge-url and leaves no half-built fam dir behind (#200).
func TestRunCloneRequiresForgeURLBeforeMutating(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fam-should-not-exist")
	err := runClone("http://gitea:3000/o/r.git", cloneOpts{dir: dir, forgeURL: "", agentsSpec: "claude=claude-code"}, io.Discard)
	if err == nil {
		t.Fatal("expected error for missing --forge-url")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("clone mutated the filesystem before validating --forge-url: %s exists", dir)
	}
}

func TestParseCloneURL(t *testing.T) {
	cases := []struct {
		url      string
		wantName string
		wantRepo string
	}{
		{"ssh://git@gitea.home.rlupi.com:2222/deep-cuts/deep-cuts.git", "deep-cuts", "deep-cuts/deep-cuts"},
		{"git@gitea:botfam/botfam.git", "botfam", "botfam/botfam"},
		{"http://gitea:3000/botfam/botfam.git", "botfam", "botfam/botfam"},
		{"deep-cuts/deep-cuts", "deep-cuts", "deep-cuts/deep-cuts"},
		{"solorepo", "solorepo", "solorepo"},
		{"", "", ""},
	}
	for _, c := range cases {
		name, repo := parseCloneURL(c.url)
		if name != c.wantName || repo != c.wantRepo {
			t.Errorf("parseCloneURL(%q) = (%q,%q), want (%q,%q)", c.url, name, repo, c.wantName, c.wantRepo)
		}
	}
}

func TestParseAgentsSpec(t *testing.T) {
	got, err := parseAgentsSpec("claude=claude-code, agy=antigravity, codex")
	if err != nil {
		t.Fatalf("parseAgentsSpec: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 agents, got %d: %v", len(got), got)
	}
	if got["claude"].Harness != "claude-code" {
		t.Errorf("claude harness = %q", got["claude"].Harness)
	}
	if got["agy"].Harness != "antigravity" {
		t.Errorf("agy harness = %q", got["agy"].Harness)
	}
	if got["codex"].Harness != "claude-code" { // bare name defaults
		t.Errorf("codex (bare) harness = %q, want claude-code default", got["codex"].Harness)
	}
	if got["claude"].Name != "claude" {
		t.Errorf("name not set: %+v", got["claude"])
	}
	if got["claude"].ForgeUser != "claude-bot" {
		t.Errorf("forge_user default = %q, want claude-bot", got["claude"].ForgeUser)
	}
}

func TestParseAgentsSpecRejectsBadName(t *testing.T) {
	if _, err := parseAgentsSpec("bad name=claude-code"); err == nil {
		t.Fatal("expected error for invalid agent name")
	}
}
