package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
)

// makeLinkedWorktree creates a main git repo under tempDir/main, creates
// "feature-branch", and adds a linked worktree at tempDir/wt-bob. It returns
// mainDir and wtDir.
func makeLinkedWorktree(t *testing.T, tempDir string) (mainDir, wtDir string) {
	t.Helper()
	mainDir = filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, mainDir)
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	runGit(mainDir, "branch", "feature-branch")
	wtDir = filepath.Join(tempDir, "wt-bob")
	runGit(mainDir, "worktree", "add", wtDir, "feature-branch")
	return mainDir, wtDir
}

func TestWorktreeCmd(t *testing.T) {
	tempDir := t.TempDir()
	mainDir, wtDir := makeLinkedWorktree(t, tempDir)

	// 1. Test "worktree init" in the main repo (should fail)
	var out bytes.Buffer
	err := cmdutil.RunCobra(NewWorktreeCmd(), []string{"init", "bob", mainDir}, &out)
	if err == nil {
		t.Fatal("expected error running worktree init on main repo, got nil")
	}
	if !strings.Contains(err.Error(), "run inside a linked worktree") {
		t.Errorf("unexpected error: %v", err)
	}

	// 2. Test "worktree init" in the linked worktree (should succeed)
	out.Reset()
	err = cmdutil.RunCobra(NewWorktreeCmd(), []string{"init", "bob", wtDir}, &out)
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
	err = cmdutil.RunCobra(NewWorktreeCmd(), []string{"sync", wtDir}, &out)
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
	err = cmdutil.RunCobra(NewWorktreeCmd(), []string{"sync", wtDir}, &out)
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

func TestWorktreeSyncWiki(t *testing.T) {
	tempDir := t.TempDir()
	_, wtDir := makeLinkedWorktree(t, tempDir)

	var out bytes.Buffer
	if err := cmdutil.RunCobra(NewWorktreeCmd(), []string{"init", "bob", wtDir}, &out); err != nil {
		t.Fatalf("worktree init failed: %v", err)
	}

	// Set up mock wiki remote
	wikiRemote := filepath.Join(tempDir, "wiki.git")
	if err := exec.Command("git", "init", "--bare", wikiRemote).Run(); err != nil {
		t.Fatalf("failed to init bare wiki remote: %v", err)
	}

	// Clone the mock wiki into wtDir/wiki
	wikiLocal := filepath.Join(wtDir, "wiki")
	if err := exec.Command("git", "clone", wikiRemote, wikiLocal).Run(); err != nil {
		t.Fatalf("failed to clone mock wiki: %v", err)
	}

	// Configure git identity in wikiLocal so we can commit
	exec.Command("git", "-C", wikiLocal, "config", "user.name", "bob").Run()
	exec.Command("git", "-C", wikiLocal, "config", "user.email", "bob@example.com").Run()

	// Push a commit to the wiki remote
	dummyFile := filepath.Join(wikiLocal, "readme.md")
	if err := os.WriteFile(dummyFile, []byte("wiki version 1"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", wikiLocal, "add", "readme.md").Run()
	exec.Command("git", "-C", wikiLocal, "commit", "-m", "first wiki commit").Run()
	exec.Command("git", "-C", wikiLocal, "push", "origin", "main").Run()

	// Now make a second clone of the wiki remote, push a change to simulate upstream updates.
	wikiUpstream := filepath.Join(tempDir, "wiki-upstream")
	if err := exec.Command("git", "clone", wikiRemote, wikiUpstream).Run(); err != nil {
		t.Fatalf("failed to clone upstream: %v", err)
	}
	exec.Command("git", "-C", wikiUpstream, "config", "user.name", "alice").Run()
	exec.Command("git", "-C", wikiUpstream, "config", "user.email", "alice@example.com").Run()
	if err := os.WriteFile(filepath.Join(wikiUpstream, "readme.md"), []byte("wiki version 2"), 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", wikiUpstream, "commit", "-am", "upstream change").Run()
	exec.Command("git", "-C", wikiUpstream, "push", "origin", "main").Run()

	// Run worktree sync in wtDir. It should sync both main repo and the wiki!
	out.Reset()
	if err := cmdutil.RunCobra(NewWorktreeCmd(), []string{"sync", wtDir}, &out); err != nil {
		t.Fatalf("worktree sync failed: %v\nOutput: %s", err, out.String())
	}

	// Assert that wikiLocal got updated to "wiki version 2"
	content, err := os.ReadFile(dummyFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "wiki version 2" {
		t.Errorf("expected wiki local to be synced to version 2, got %q", string(content))
	}
}
