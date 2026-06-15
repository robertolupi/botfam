package forge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewClientTestTokenFallbackIsOptIn verifies that NewClient does NOT fall
// back to a token-botfam-<actor>-test credential by default (#70), and only
// uses it when BOTFAM_ALLOW_TEST_TOKEN_FALLBACK=1. Forge endpoint resolution is
// short-circuited via GITEA_* env so the test exercises only token resolution.
func TestNewClientTestTokenFallbackIsOptIn(t *testing.T) {
	const actor = "alice"

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".botfam"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the *-test token exists — no normal (token-testfam-alice) or legacy
	// (token-botfam-alice) file.
	testToken := filepath.Join(home, ".botfam", "token-botfam-"+actor+"-test")
	if err := os.WriteFile(testToken, []byte("test-cred\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	t.Setenv("GITEA_TOKEN", "") // force file-based resolution
	// Resolve the forge endpoint from env so NewClient never touches git.
	t.Setenv("GITEA_URL", "http://example.test")
	t.Setenv("GITEA_OWNER", "o")
	t.Setenv("GITEA_REPO", "r")

	workDir := t.TempDir()

	t.Run("default: test token is ignored, fails closed", func(t *testing.T) {
		t.Setenv("BOTFAM_ALLOW_TEST_TOKEN_FALLBACK", "")
		if _, err := NewClient(workDir, actor); err == nil {
			t.Fatal("expected NewClient to fail (no production token), but it succeeded — the *-test fallback must not apply by default")
		}
	})

	t.Run("opt-in: test token used when explicitly allowed", func(t *testing.T) {
		t.Setenv("BOTFAM_ALLOW_TEST_TOKEN_FALLBACK", "1")
		c, err := NewClient(workDir, actor)
		if err != nil {
			t.Fatalf("expected NewClient to succeed with BOTFAM_ALLOW_TEST_TOKEN_FALLBACK=1, got: %v", err)
		}
		if c.Token != "test-cred" {
			t.Errorf("token = %q, want %q (from the *-test file)", c.Token, "test-cred")
		}
	})
}

func TestHarnessTokenPath(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	got, err := HarnessTokenPath("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/home/test", ".botfam", "token-claude-code") {
		t.Errorf("path = %q", got)
	}
	if _, err := HarnessTokenPath(""); err == nil {
		t.Error("expected error for empty harness")
	}
}
