package forge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewClientNoLegacyFallback is the #183 regression: a present legacy
// token-botfam-<actor> (or per-fam token-<fam>-<actor>) must NOT be silently
// used to satisfy a missing per-harness token. NewClient must fail closed.
func TestNewClientNoLegacyFallback(t *testing.T) {
	const actor = "alice"
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".botfam"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the legacy + per-fam tokens exist — the exact files the old fallback
	// chain would have used.
	for _, name := range []string{"token-botfam-" + actor, "token-testfam-" + actor} {
		if err := os.WriteFile(filepath.Join(home, ".botfam", name), []byte("legacy\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("BOTFAM_CONFIG", "") // config resolves under the temp HOME (absent)
	t.Setenv("GITEA_TOKEN", "")
	t.Setenv("GITEA_URL", "http://example.test")
	t.Setenv("GITEA_OWNER", "o")
	t.Setenv("GITEA_REPO", "r")
	t.Setenv("BOTFAM_ALLOW_TEST_TOKEN_FALLBACK", "")

	workDir := t.TempDir() // no fam.toml → no harness
	if _, err := NewClientForWorkDir(workDir, actor); err == nil {
		t.Fatal("expected fail-closed: legacy/per-fam tokens must not be a silent fallback for a missing per-harness token (#183)")
	}
}
