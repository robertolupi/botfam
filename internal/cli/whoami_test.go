package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhoamiCmd(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "botfam-whoami-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	gitDir := filepath.Join(tempDir, "myrepo")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	wtDir := filepath.Join(gitDir, "wt-bob")
	if err := os.Mkdir(wtDir, 0755); err != nil {
		t.Fatal(err)
	}

	nestedSubDir := filepath.Join(wtDir, "internal", "fam")
	if err := os.MkdirAll(nestedSubDir, 0755); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	if err := os.Chdir(nestedSubDir); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = WhoamiCmd([]string{}, &out)
	if err != nil {
		t.Fatalf("WhoamiCmd failed: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != "bob" {
		t.Errorf("WhoamiCmd output = %q, want %q", got, "bob")
	}
}
