package fam

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGoRepo creates a minimal buildable+testable Go module repo at dir with a
// single committed commit and returns its HEAD sha.
func initGoRepo(t *testing.T, dir string, goFile, testFile string) string {
	t.Helper()
	runCmd := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to run %s %v: %v\n%s", name, args, err, out)
		}
	}

	runCmd("git", "init")
	runCmd("git", "config", "user.name", "test")
	runCmd("git", "config", "user.email", "test@example.com")

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module verifytest\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goFile), 0644); err != nil {
		t.Fatal(err)
	}
	if testFile != "" {
		if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(testFile), 0644); err != nil {
			t.Fatal(err)
		}
	}

	runCmd("git", "add", "-A")
	runCmd("git", "commit", "-m", "initial")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

const goodMain = `package main

func Add(a, b int) int { return a + b }

func main() {}
`

const goodTest = `package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
`

func runVerifyIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	// VerifyCmd resolves RepoPath(".") from the process working directory.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()

	var buf bytes.Buffer
	cmdErr := VerifyCmd(args, &buf)
	return buf.String(), cmdErr
}

func TestVerifyPass(t *testing.T) {
	dir := t.TempDir()
	sha := initGoRepo(t, dir, goodMain, goodTest)

	out, err := runVerifyIn(t, dir, sha)
	if err != nil {
		t.Fatalf("verify should pass, got error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "RESULT: PASS") {
		t.Errorf("expected PASS in output, got:\n%s", out)
	}
}

func TestVerifyTestFailure(t *testing.T) {
	badTest := `package main

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) == 3 {
		t.Fatal("intentional failure")
	}
}
`
	dir := t.TempDir()
	sha := initGoRepo(t, dir, goodMain, badTest)

	out, err := runVerifyIn(t, dir, sha)
	if err == nil {
		t.Fatalf("verify should fail on failing tests, output:\n%s", out)
	}
	if !strings.Contains(out, "RESULT: FAIL") {
		t.Errorf("expected FAIL in output, got:\n%s", out)
	}
}

func TestVerifyBuildFailure(t *testing.T) {
	badMain := `package main

func main() { this is not valid go }
`
	dir := t.TempDir()
	sha := initGoRepo(t, dir, badMain, "")

	out, err := runVerifyIn(t, dir, sha)
	if err == nil {
		t.Fatalf("verify should fail on broken build, output:\n%s", out)
	}
	if !strings.Contains(out, "RESULT: FAIL") {
		t.Errorf("expected FAIL in output, got:\n%s", out)
	}
}

func TestVerifyBadSha(t *testing.T) {
	dir := t.TempDir()
	initGoRepo(t, dir, goodMain, goodTest)

	_, err := runVerifyIn(t, dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err == nil {
		t.Fatal("verify should fail on an unknown sha")
	}
	if !strings.Contains(err.Error(), "cannot resolve") {
		t.Errorf("expected resolve error, got: %v", err)
	}
}

func TestVerifyMissingSha(t *testing.T) {
	var buf bytes.Buffer
	if err := VerifyCmd(nil, &buf); err == nil {
		t.Fatal("expected usage error when sha missing")
	}
}

func TestVerifyCleansUpWorktree(t *testing.T) {
	dir := t.TempDir()
	sha := initGoRepo(t, dir, goodMain, goodTest)

	if _, err := runVerifyIn(t, dir, sha); err != nil {
		t.Fatalf("verify failed: %v", err)
	}

	// No lingering linked worktrees should remain registered.
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	// Only the main worktree (dir itself) should be listed.
	if strings.Count(string(out), "worktree ") != 1 {
		t.Errorf("expected only the main worktree to remain, got:\n%s", out)
	}
}
