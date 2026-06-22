package ops_test

import (
	"bytes"
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/cli/ops"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"github.com/robertolupi/botfam/internal/gitexec"
)

// TestSprintStartFlags covers the validation that happens before any
// forge/session work. Successful seeding (which needs a forge client) is covered
// white-box in sprint_start_test.go.
func TestSprintStartFlags(t *testing.T) {
	var out bytes.Buffer

	// Missing flags should fail.
	cmd := ops.NewSprintCmd()
	if err := cmdutil.RunCobra(cmd, []string{"start", "sprint-1"}, &out); err == nil {
		t.Fatal("expected start command to fail without milestone or issues")
	}

	// Both flags is ambiguous and should fail.
	out.Reset()
	cmd = ops.NewSprintCmd()
	if err := cmdutil.RunCobra(cmd, []string{"start", "sprint-1", "--milestone", "42", "--issues", "12,34"}, &out); err == nil {
		t.Fatal("expected start command to fail when both --milestone and --issues are given")
	}
}

// TestSprintRunRequiresStartedSession verifies the start-creates/run-requires
// contract: `sprint run` errors clearly when no session was started.
func TestSprintRunRequiresStartedSession(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	var out bytes.Buffer
	cmd := ops.NewSprintCmd()
	err := cmdutil.RunCobra(cmd, []string{"run", "never-started"}, &out)
	if err == nil {
		t.Fatal("expected run to fail for a session that was never started")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want a 'not found' message pointing at sprint start", err)
	}
}

func TestSprintRunLeaseAcquisition(t *testing.T) {
	// Setup user home directory redirect to a temp dir so we don't pollute real ~/.botfam
	tmpHome, err := ioutil.TempDir("", "botfam-home-cli-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	// Create a mock git repository to satisfy ResolveRepoName
	tempDir, err := os.MkdirTemp("", "botfam-sprint-run-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	gitDir := filepath.Join(tempDir, "botfam")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, gitDir)

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(oldWd)
	}()

	if err := os.Chdir(gitDir); err != nil {
		t.Fatal(err)
	}

	// `sprint run` now requires a session created by `sprint start`. Pre-create an
	// empty session store under the redirected HOME (start itself needs a forge
	// client, exercised white-box elsewhere).
	sessionDir := filepath.Join(tmpHome, ".botfam", "sessions", "sprint-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitexec.One(sessionDir, "init"); err != nil {
		t.Fatal(err)
	}
	_, _ = gitexec.Output(sessionDir, "config", "user.name", "botfam-supervisor")
	_, _ = gitexec.Output(sessionDir, "config", "user.email", "supervisor@botfam.invalid")
	if err := store.EnsureSessionGitignore(sessionDir, singlehost.SessionRepoGitignorePatterns()...); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(sessionDir, "session.db"))
	if err != nil {
		t.Fatalf("pre-create session store: %v", err)
	}
	if err := store.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	_ = db.Close()

	// Run sprint run cmd with a canceled context so it exits immediately after acquiring lease
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	cmd1 := ops.NewSprintCmd()
	cmd1.SetContext(ctx)

	var out1 bytes.Buffer
	err = cmdutil.RunCobra(cmd1, []string{"run", "sprint-1"}, &out1)
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	got1 := out1.String()
	if !strings.Contains(got1, "Sprint run started") {
		t.Errorf("expected lease acquired output, got: %q", got1)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	_, err := gitexec.One(dir, "init")
	if err != nil {
		t.Fatal(err)
	}
	// We need at least one commit for rev-list to succeed
	if err := ioutil.WriteFile(filepath.Join(dir, "dummy.txt"), []byte("init"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = gitexec.Output(dir, "add", "dummy.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = gitexec.Output(dir, "commit", "-m", "initial commit")
	if err != nil {
		t.Fatal(err)
	}
}
