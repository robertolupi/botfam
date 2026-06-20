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
	"github.com/robertolupi/botfam/internal/gitexec"
)

func TestSprintStartFlags(t *testing.T) {
	var out bytes.Buffer

	// 1. Missing flags should fail
	cmd := ops.NewSprintCmd()
	err := cmdutil.RunCobra(cmd, []string{"start", "sprint-1"}, &out)
	if err == nil {
		t.Fatal("expected start command to fail without milestone or issues")
	}

	// 2. Milestone flag
	out.Reset()
	cmd = ops.NewSprintCmd()
	err = cmdutil.RunCobra(cmd, []string{"start", "sprint-1", "--milestone", "42"}, &out)
	if err != nil {
		t.Fatalf("start milestone failed: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Milestone=42") {
		t.Errorf("unexpected output: %s", got)
	}

	// 3. Issues flag
	out.Reset()
	cmd = ops.NewSprintCmd()
	err = cmdutil.RunCobra(cmd, []string{"start", "sprint-1", "--issues", "12,34"}, &out)
	if err != nil {
		t.Fatalf("start issues failed: %v", err)
	}
	got = out.String()
	if !strings.Contains(got, "Issues=[12 34]") {
		t.Errorf("unexpected output: %s", got)
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
