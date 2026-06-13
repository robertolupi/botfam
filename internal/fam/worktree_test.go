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

func TestWorktreeRegister(t *testing.T) {
	tempDir := t.TempDir()
	eval := func(p string) string {
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			return rp
		}
		return p
	}

	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, mainDir)

	// Two linked worktrees that the registry doesn't know about yet.
	wantPaths := map[string]bool{eval(mainDir): true}
	for _, name := range []string{"wt-a", "wt-b"} {
		branch := "br-" + name
		cmd := exec.Command("git", "branch", branch)
		cmd.Dir = mainDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("branch %s: %v", branch, err)
		}
		wt := filepath.Join(tempDir, name)
		cmd = exec.Command("git", "worktree", "add", wt, branch)
		cmd.Dir = mainDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("worktree add %s: %v", name, err)
		}
		wantPaths[eval(wt)] = true
	}

	// A worktree NESTED inside main (mimics a harness's ephemeral agent
	// worktree under main/.claude/worktrees/...): must be excluded.
	nestedBranch := exec.Command("git", "branch", "br-nested")
	nestedBranch.Dir = mainDir
	if err := nestedBranch.Run(); err != nil {
		t.Fatalf("branch nested: %v", err)
	}
	nested := filepath.Join(mainDir, ".claude", "worktrees", "agent-x")
	nestedAdd := exec.Command("git", "worktree", "add", nested, "br-nested")
	nestedAdd.Dir = mainDir
	if err := nestedAdd.Run(); err != nil {
		t.Fatalf("worktree add nested: %v", err)
	}

	// Fam root whose registry starts knowing only the main checkout.
	root := filepath.Join(tempDir, "famroot")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(root, "fam.toml")
	if err := WriteRegistry(regPath, Registry{
		Name: "test", CreatedAt: "now", RepoPaths: []string{eval(mainDir)},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLLAB_ROOT", root)

	// First register: should add the two missing worktrees.
	var out bytes.Buffer
	if err := WorktreeCmd([]string{"register", mainDir}, &out); err != nil {
		t.Fatalf("register failed: %v\n%s", err, out.String())
	}
	reg, err := ReadRegistry(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.RepoPaths) != len(wantPaths) {
		t.Fatalf("repo_paths = %v, want %d unique paths", reg.RepoPaths, len(wantPaths))
	}
	for _, p := range reg.RepoPaths {
		if !wantPaths[p] {
			t.Errorf("unexpected repo_path %q", p)
		}
		delete(wantPaths, p)
	}
	if len(wantPaths) != 0 {
		t.Errorf("missing repo_paths: %v", wantPaths)
	}

	// Second register: idempotent, nothing added.
	out.Reset()
	if err := WorktreeCmd([]string{"register", mainDir}, &out); err != nil {
		t.Fatalf("second register failed: %v", err)
	}
	if !strings.Contains(out.String(), "already current") {
		t.Errorf("expected idempotent message, got: %s", out.String())
	}
}
