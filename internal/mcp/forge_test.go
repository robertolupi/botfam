package mcp

import (
	"strings"
	"testing"
)

// TestForgeToolsMounted verifies the gitea-mcp operations are registered as
// botfam subtools under the forge_ prefix, alongside (not colliding with) the
// native tools (#429).
func TestForgeToolsMounted(t *testing.T) {
	s := &server{}
	entries := s.buildEntries()

	// Native tools survive unprefixed.
	for _, native := range []string{"worktree_sync", "worktree_init", "orient"} {
		if _, ok := entries[native]; !ok {
			t.Errorf("native tool %q missing from registry", native)
		}
	}

	// A representative spread of forge tools is present, all forge_-prefixed.
	var forgeCount int
	for name := range entries {
		if strings.HasPrefix(name, forgeToolPrefix) {
			forgeCount++
		}
	}
	if forgeCount == 0 {
		t.Fatal("no forge_* tools registered")
	}
	for _, want := range []string{"forge_issue_read", "forge_issue_write", "forge_list_issues"} {
		if _, ok := entries[want]; !ok {
			t.Errorf("expected forge tool %q in registry (got %d forge tools)", want, forgeCount)
		}
	}
}

// TestForgeReadToolsCrossActorSafe verifies read-only forge tools are marked
// cross-actor-safe while writes are not, so a forge write from another agent's
// worktree is blocked by the shared cross-actor rule (#429).
func TestForgeReadToolsCrossActorSafe(t *testing.T) {
	s := &server{}
	entries := s.buildEntries()
	if !entries["forge_issue_read"].readOnly {
		t.Error("forge_issue_read should be cross-actor read-only")
	}
	if !entries["forge_list_issues"].readOnly {
		t.Error("forge_list_issues should be cross-actor read-only")
	}
	if entries["forge_issue_write"].readOnly {
		t.Error("forge_issue_write must NOT be cross-actor read-only (it mutates)")
	}
}
