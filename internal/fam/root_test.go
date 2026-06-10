package fam

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s %v: %v", name, args, err)
		}
	}
	runCmd("git", "init")
	runCmd("git", "config", "user.name", "test")
	runCmd("git", "config", "user.email", "test@example.com")
	runCmd("git", "commit", "--allow-empty", "-m", "initial commit")
}

func TestParseActor(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"wt-claude", "claude"},
		{"botfam-codex", "codex"},
		{"wt-my-agent", "my-agent"},
		{"deep-cuts", ""}, // no wt-/botfam- prefix: fail closed, no actor
		{"myrepo", ""},
		{"wt-", ""}, // empty remainder after prefix: no actor
		{"botfam-", ""},
		{"wt-bad.name", ""}, // remainder fails the store name validator
	}
	for _, tc := range cases {
		if got := ParseActor(tc.base); got != tc.want {
			t.Errorf("ParseActor(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestResolver(t *testing.T) {
	// The Resolver getenv falls back to os.Getenv even when Env is non-nil
	// (known issue L2); pin process env so the test is deterministic.
	t.Setenv("COLLAB_ROOT", "")
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_FAM", "")

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

	// Case 2: Inside a git repository
	gitDir := filepath.Join(tempDir, "myrepo")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	// A worktree subdirectory with actor suffix
	wtDir := filepath.Join(gitDir, "wt-bob")
	if err := os.Mkdir(wtDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Resolve from main checkout directory
	rMain := Resolver{
		WorkDir: gitDir,
		Env:     []string{},
	}
	infoMain, err := rMain.Resolve()
	if err != nil {
		t.Fatalf("resolve from main checkout failed: %v", err)
	}

	// Resolve from worktree directory
	rWt := Resolver{
		WorkDir: wtDir,
		Env:     []string{},
	}
	infoWt, err := rWt.Resolve()
	if err != nil {
		t.Fatalf("resolve from worktree failed: %v", err)
	}

	// Assert they both resolved to the same root path!
	if infoMain.Root != infoWt.Root {
		t.Errorf("split brain! main resolved to %q, worktree resolved to %q", infoMain.Root, infoWt.Root)
	}

	// Assert actor parsing only happened in the worktree
	if infoMain.Actor != "" {
		t.Errorf("expected main Actor to be empty, got %q", infoMain.Actor)
	}
	if infoWt.Actor != "bob" {
		t.Errorf("expected worktree Actor to be %q, got %q", "bob", infoWt.Actor)
	}

	// Assert the root is derived from git history (starts with fam-)
	if !strings.HasPrefix(infoMain.Name, "fam-") {
		t.Errorf("expected family name to start with 'fam-', got %q", infoMain.Name)
	}

	// Case 3: Outside a git repository
	nonGitDir := filepath.Join(tempDir, "nongit")
	if err := os.Mkdir(nonGitDir, 0755); err != nil {
		t.Fatal(err)
	}
	rNonGit := Resolver{
		WorkDir: nonGitDir,
		Env:     []string{},
	}
	_, err = rNonGit.Resolve()
	if err == nil {
		t.Fatal("expected resolve outside git repository to fail")
	}
	expectedErrSub := "COLLAB_ROOT is unset and no git history could be used to derive a fam root"
	if !strings.Contains(err.Error(), expectedErrSub) {
		t.Errorf("expected error containing %q, got %q", expectedErrSub, err.Error())
	}
}
