package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCmdScaffoldsTomlAndGit(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "testproject")

	var out bytes.Buffer
	err := runInit(projectDir, &out)
	if err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// Verify fam.toml exists and has correct name/slug
	tomlPath := filepath.Join(projectDir, "fam.toml")
	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		t.Fatalf("fam.toml was not scaffolded")
	}

	reg, err := ReadRegistry(tomlPath)
	if err != nil {
		t.Fatalf("failed to read scaffolded registry: %v", err)
	}

	if reg.Name != "testproject" {
		t.Errorf("expected registry name 'testproject', got %q", reg.Name)
	}
	if reg.Slug != "testproject" {
		t.Errorf("expected registry slug 'testproject', got %q", reg.Slug)
	}

	// Verify git repo in main/ was initialized
	gitDir := filepath.Join(projectDir, "main", ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Fatalf("Git repository in main/ was not initialized")
	}
}
