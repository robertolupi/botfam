package cli

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
		base     string
		repoName string
		want     string
	}{
		{"wt-claude", "botfam", "claude"},
		{"botfam-codex", "botfam", "codex"},
		{"wt-my-agent", "botfam", "my-agent"},
		{"deep-cuts-agy", "deep-cuts", "agy"},
		{"wt-deep-cuts-claude", "deep-cuts", "claude"},
		{"deep-cuts", "deep-cuts", ""}, // no actor remainder
		{"myrepo", "botfam", ""},
		{"wt-", "botfam", ""}, // empty remainder
		{"botfam-", "botfam", ""},
		{"wt-bad.name", "botfam", ""},
	}
	for _, tc := range cases {
		if got := ParseActor(tc.base, tc.repoName); got != tc.want {
			t.Errorf("ParseActor(%q, %q) = %q, want %q", tc.base, tc.repoName, got, tc.want)
		}
	}
}

func TestResolver(t *testing.T) {
	// The Resolver getenv falls back to os.Getenv even when Env is non-nil
	// (known issue L2); pin process env so the test is deterministic.
	t.Setenv("COLLAB_ACTOR", "")
	t.Setenv("BOTFAM_FAM", "")

	tempDir, err := os.MkdirTemp("", "botfam-resolver-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

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
	rMain := GitResolver{
		Env:     []string{},
	}
	infoMain, err := rMain.ResolveIdentity(gitDir)
	if err != nil {
		t.Fatalf("resolve from main checkout failed: %v", err)
	}

	// Resolve from worktree directory
	rWt := GitResolver{
		Env:     []string{},
	}
	infoWt, err := rWt.ResolveIdentity(wtDir)
	if err != nil {
		t.Fatalf("resolve from worktree failed: %v", err)
	}

	// Assert they both resolved to the same root path!
	if infoMain.FamDir != infoWt.FamDir {
		t.Errorf("split brain! main resolved to %q, worktree resolved to %q", infoMain.FamDir, infoWt.FamDir)
	}

	// Assert actor parsing only happened in the worktree
	if infoMain.Actor != "" {
		t.Errorf("expected main Actor to be empty, got %q", infoMain.Actor)
	}
	if infoWt.Actor != "bob" {
		t.Errorf("expected worktree Actor to be %q, got %q", "bob", infoWt.Actor)
	}

	// A nested subdirectory inside the worktree
	nestedSubDir := filepath.Join(wtDir, "cmd", "botfam")
	if err := os.MkdirAll(nestedSubDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Resolve from the nested subdirectory inside the worktree
	rSub := GitResolver{
		Env:     []string{},
	}
	infoSub, err := rSub.ResolveIdentity(nestedSubDir)
	if err != nil {
		t.Fatalf("resolve from nested subdirectory failed: %v", err)
	}
	if infoSub.Actor != "bob" {
		t.Errorf("expected subdirectory Actor to resolve to %q, got %q", "bob", infoSub.Actor)
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
	rNonGit := GitResolver{
		Env:     []string{},
	}
	_, err = rNonGit.ResolveIdentity(nonGitDir)
	if err == nil {
		t.Fatal("expected resolve outside git repository to fail")
	}
	expectedErrSub := "not inside a fam worktree and no git history could be used to derive a fam root"
	if !strings.Contains(err.Error(), expectedErrSub) {
		t.Errorf("expected error containing %q, got %q", expectedErrSub, err.Error())
	}

	// Case 4: Inside a git repository under unified layout (fam.toml in parent)
	unifiedDir := filepath.Join(tempDir, "unified-fam")
	if err := os.Mkdir(unifiedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if eval, err := filepath.EvalSymlinks(unifiedDir); err == nil {
		unifiedDir = eval
	}
	famTOMLContent := `name = "my-unified-fam"
slug = "muf"
roster = ["bob"]

[agent.bob]
harness = "test-harness"
`
	if err := os.WriteFile(filepath.Join(unifiedDir, "fam.toml"), []byte(famTOMLContent), 0644); err != nil {
		t.Fatal(err)
	}
	wtBobDir := filepath.Join(unifiedDir, "bob")
	if err := os.Mkdir(wtBobDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, wtBobDir)

	rUnified := GitResolver{
		Env:     []string{},
	}
	infoUnified, err := rUnified.ResolveIdentity(wtBobDir)
	if err != nil {
		t.Fatalf("resolve under unified layout failed: %v", err)
	}
	if infoUnified.FamDir != unifiedDir {
		t.Errorf("expected unified root %q, got %q", unifiedDir, infoUnified.FamDir)
	}
	if infoUnified.Name != "my-unified-fam" {
		t.Errorf("expected unified name %q, got %q", "my-unified-fam", infoUnified.Name)
	}
	if infoUnified.Actor != "bob" {
		t.Errorf("expected unified actor %q, got %q", "bob", infoUnified.Actor)
	}
}
