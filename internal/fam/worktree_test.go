package fam

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeCmd(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, mainDir)

	// Create a branch to check out in the worktree
	cmd := exec.Command("git", "branch", "feature-branch")
	cmd.Dir = mainDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	// Create linked worktree
	wtDir := filepath.Join(tempDir, "wt-bob")
	cmd = exec.Command("git", "worktree", "add", wtDir, "feature-branch")
	cmd.Dir = mainDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add worktree: %v", err)
	}

	// 1. Test "worktree init" in the main repo (should fail)
	var out bytes.Buffer
	err := WorktreeCmd([]string{"init", "bob", mainDir}, &out)
	if err == nil {
		t.Fatal("expected error running worktree init on main repo, got nil")
	}
	if !strings.Contains(err.Error(), "run inside a linked worktree") {
		t.Errorf("unexpected error: %v", err)
	}

	// 2. Test "worktree init" in the linked worktree (should succeed)
	out.Reset()
	err = WorktreeCmd([]string{"init", "bob", wtDir}, &out)
	if err != nil {
		t.Fatalf("worktree init failed: %v", err)
	}
	if !strings.Contains(out.String(), "Worktree identity successfully set") {
		t.Errorf("unexpected output: %s", out.String())
	}

	// Verify that user.name config is set on the worktree
	nameCmd := exec.Command("git", "config", "--worktree", "user.name")
	nameCmd.Dir = wtDir
	nameOut, err := nameCmd.Output()
	if err != nil {
		t.Fatalf("failed to get user.name config: %v", err)
	}
	if strings.TrimSpace(string(nameOut)) != "bob" {
		t.Errorf("expected user.name config to be 'bob', got %q", string(nameOut))
	}

	// 3. Test "worktree sync" (should succeed when clean)
	out.Reset()
	err = WorktreeCmd([]string{"sync", wtDir}, &out)
	if err != nil {
		t.Fatalf("worktree sync failed: %v", err)
	}
	if !strings.Contains(out.String(), "Merging main into branch") {
		t.Errorf("unexpected output: %s", out.String())
	}

	// 4. Test "worktree sync" with dirty working tree (should auto-stash and merge)
	// Modify a file in the worktree
	dirtyFile := filepath.Join(wtDir, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	err = WorktreeCmd([]string{"sync", wtDir}, &out)
	if err != nil {
		t.Fatalf("worktree sync with dirty tree failed: %v", err)
	}
	if !strings.Contains(out.String(), "Working tree is dirty. Automatically stashing local changes...") {
		t.Errorf("expected auto-stash message, got output: %s", out.String())
	}
	if !strings.Contains(out.String(), "Popping stashed local changes...") {
		t.Errorf("expected stash pop message, got output: %s", out.String())
	}

	// Verify the dirty file is still present
	content, err := os.ReadFile(dirtyFile)
	if err != nil {
		t.Fatalf("dirty file disappeared: %v", err)
	}
	if string(content) != "dirty" {
		t.Errorf("expected dirty file content 'dirty', got %q", string(content))
	}
}
