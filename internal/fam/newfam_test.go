package fam

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNewfam(t *testing.T) {
	tempDir := t.TempDir()
	mainDir := filepath.Join(tempDir, "main")
	if err := os.Mkdir(mainDir, 0755); err != nil {
		t.Fatal(err)
	}

	initGitRepo(t, mainDir)

	// Set environment variables for the test
	collabRoot := filepath.Join(tempDir, "collab")
	t.Setenv("COLLAB_ROOT", collabRoot)
	t.Setenv("USER", "testoperator")
	t.Setenv("HOME", tempDir)

	// Create mock home .botfam directory to hold symlinks
	if err := os.MkdirAll(filepath.Join(tempDir, ".botfam"), 0755); err != nil {
		t.Fatal(err)
	}

	// Change directory to main repo root
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(mainDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Run NewfamCmd
	var out bytes.Buffer
	args := []string{"myproject", "--agents", "agy,claude"}
	if err := NewfamCmd(args, &out); err != nil {
		t.Fatalf("NewfamCmd failed: %v\nOutput:\n%s", err, out.String())
	}

	// Check if the registry fam.toml was written correctly
	regPath := filepath.Join(collabRoot, "fam.toml")
	reg, err := ReadRegistry(regPath)
	if err != nil {
		t.Fatalf("failed to read registry at %s: %v", regPath, err)
	}
	if reg.Name != "myproject" {
		t.Errorf("expected registry name 'myproject', got %q", reg.Name)
	}

	// Verify that roster contains all agents and the operator
	expectedRoster := []string{"agy", "claude", "testoperator"}
	rosterMap := make(map[string]bool)
	for _, member := range reg.Roster {
		rosterMap[member] = true
	}
	for _, member := range expectedRoster {
		if !rosterMap[member] {
			t.Errorf("missing %q from roster: %v", member, reg.Roster)
		}
	}

	// Verify that the worktree directories exist
	for _, actor := range expectedRoster {
		wtDir := filepath.Join(tempDir, "wt-"+actor)
		if _, err := os.Stat(wtDir); os.IsNotExist(err) {
			t.Errorf("worktree directory %s does not exist", wtDir)
		}

		// Verify Claude settings file
		claudeSettings := filepath.Join(wtDir, ".claude", "settings.json")
		if _, err := os.Stat(claudeSettings); os.IsNotExist(err) {
			t.Errorf("claude settings %s does not exist", claudeSettings)
		}

		// Verify Agent docs files
		for _, docName := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
			docPath := filepath.Join(wtDir, docName)
			if _, err := os.Stat(docPath); os.IsNotExist(err) {
				t.Errorf("agent doc %s does not exist", docPath)
			}
		}
	}
}
