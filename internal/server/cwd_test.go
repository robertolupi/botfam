package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindGitRoot(t *testing.T) {
	temp := t.TempDir()

	// Create a mock git root
	gitRoot := filepath.Join(temp, "myrepo")
	if err := os.Mkdir(gitRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(gitRoot, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create nested subdirectories
	subDir := filepath.Join(gitRoot, "pkg", "subpkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Check findGitRoot from nested subpkg
	root, err := findGitRoot(subDir)
	if err != nil {
		t.Fatalf("expected to find git root, got: %v", err)
	}
	if root != gitRoot {
		t.Fatalf("expected git root %s, got %s", gitRoot, root)
	}

	// Check findGitRoot from gitRoot itself
	root, err = findGitRoot(gitRoot)
	if err != nil {
		t.Fatalf("expected to find git root, got: %v", err)
	}
	if root != gitRoot {
		t.Fatalf("expected git root %s, got %s", gitRoot, root)
	}

	// Check findGitRoot on a directory without .git
	_, err = findGitRoot(temp)
	if err == nil {
		t.Fatalf("expected error finding git root on directory without .git")
	}
}

func TestGetProcessCWD(t *testing.T) {
	pid := os.Getpid()
	cwd, err := getProcessCWD(pid)
	if err != nil {
		t.Fatalf("expected to get CWD for self, got: %v", err)
	}

	expected, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// Evaluate symlinks in case of temp dir symlinks (like /var -> /private/var on macOS)
	evalCwd, _ := filepath.EvalSymlinks(cwd)
	evalExpected, _ := filepath.EvalSymlinks(expected)

	if evalCwd != evalExpected {
		t.Fatalf("expected CWD %s, got %s", evalExpected, evalCwd)
	}
}
