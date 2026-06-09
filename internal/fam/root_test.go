package fam

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolver(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "botfam-resolver-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Case 1: Explicit COLLAB_ROOT env var
	env := []string{"COLLAB_ROOT=" + tempDir}
	r := Resolver{
		WorkDir: tempDir,
		Env:     env,
	}
	info, err := r.Resolve()
	if err != nil {
		t.Fatalf("explicit COLLAB_ROOT failed: %v", err)
	}
	if info.Root != tempDir {
		t.Errorf("expected Root %q, got %q", tempDir, info.Root)
	}
	if !info.Explicit {
		t.Error("expected Explicit to be true")
	}

	// Case 2: Dir matches family-actor pattern (e.g. myfam-bob)
	famActorDir := filepath.Join(tempDir, "myfam-bob")
	if err := os.Mkdir(famActorDir, 0755); err != nil {
		t.Fatal(err)
	}
	r2 := Resolver{
		WorkDir: famActorDir,
		Env:     []string{},
	}
	info2, err := r2.Resolve()
	if err != nil {
		t.Fatalf("family-actor dir failed: %v", err)
	}
	home, _ := os.UserHomeDir()
	expectedRoot := filepath.Join(home, ".botfam", "myfam")
	if info2.Root != expectedRoot {
		t.Errorf("expected Root %q, got %q", expectedRoot, info2.Root)
	}
	if info2.Actor != "bob" {
		t.Errorf("expected Actor %q, got %q", "bob", info2.Actor)
	}
}
