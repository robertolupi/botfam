package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCmdScaffoldsGit(t *testing.T) {
	tempDir := t.TempDir()
	projectDir := filepath.Join(tempDir, "testproject")

	var out bytes.Buffer
	err := runInit(projectDir, &out)
	if err != nil {
		t.Fatalf("runInit failed: %v", err)
	}

	// init no longer scaffolds a per-fam fam.toml (#404); config is global.
	if _, err := os.Stat(filepath.Join(projectDir, "fam.toml")); !os.IsNotExist(err) {
		t.Errorf("init should not scaffold a fam.toml; stat err=%v", err)
	}

	// Verify git repo in main/ was initialized.
	gitDir := filepath.Join(projectDir, "main", ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		t.Fatalf("Git repository in main/ was not initialized")
	}
}
