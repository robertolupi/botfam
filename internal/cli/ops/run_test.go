package ops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
)

type fakeIssueClient struct {
	issue *forge.Issue
	err   error
}

func (f fakeIssueClient) GetIssue(_ context.Context, _ int) (*forge.Issue, error) {
	return f.issue, f.err
}

func testRunContext(t *testing.T, worktree string) famctx.Context {
	t.Helper()
	return famctx.Context{
		FamIdentity:  famconfig.FamIdentity{FamDir: filepath.Dir(worktree)},
		WorktreeRoot: worktree,
		Registry:     famconfig.Registry{ForgeURL: "http://gitea:3000", Repository: "botfam/botfam"},
	}
}

func runJSONPath(runDir string) string {
	return filepath.Join(runDir, "run.json")
}

func mustReadRunEnvelope(t *testing.T, runDir string) runEnvelope {
	t.Helper()
	b, err := os.ReadFile(runJSONPath(runDir))
	if err != nil {
		t.Fatal(err)
	}
	var env runEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func findRunDir(t *testing.T, captureDir string) string {
	t.Helper()
	entries, err := os.ReadDir(captureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "run-") {
			return filepath.Join(captureDir, entry.Name())
		}
	}
	t.Fatalf("no run directory in %s", captureDir)
	return ""
}

func TestRunIssueSuccessWritesArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   42,
		Title:   "Test issue",
		Body:    "Some body",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/42",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      42,
		target:     "success",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.FailureClass != runStatusSuccess {
		t.Fatalf("FailureClass = %q, want %q", env.FailureClass, runStatusSuccess)
	}
	if env.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", env.ExitCode)
	}
	if env.IssueNumber != 42 {
		t.Fatalf("IssueNumber = %d, want 42", env.IssueNumber)
	}
	if env.IssueURL != client.issue.HTMLURL {
		t.Fatalf("IssueURL = %q, want %q", env.IssueURL, client.issue.HTMLURL)
	}

	stdout, err := os.ReadFile(filepath.Join(runDir, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stdout), "=") {
		t.Fatalf("unexpected stdout from shell run: %q", string(stdout))
	}

	if _, err := os.Stat(filepath.Join(runDir, "prompt.md")); err != nil {
		t.Fatalf("missing prompt.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "artifacts.json")); err != nil {
		t.Fatalf("missing artifacts.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "env.redacted.json")); err != nil {
		t.Fatalf("missing env.redacted.json: %v", err)
	}
}

func TestRunIssueToolFailureWritesFailureArtifact(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 7, Title: "Bad issue"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      7,
		target:     "fail",
		captureDir: outDir,
	})
	if err == nil {
		t.Fatalf("runIssue succeeded unexpectedly")
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.FailureClass != runStatusToolError {
		t.Fatalf("FailureClass = %q, want %q", env.FailureClass, runStatusToolError)
	}
	if env.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", env.ExitCode)
	}
}

func TestRunIssueShellCommand(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 12, Title: "Shell command"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      12,
		target:     "shell:printf shell-ok",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.FailureClass != runStatusSuccess {
		t.Fatalf("FailureClass = %q, want %q", env.FailureClass, runStatusSuccess)
	}
	stdout, err := os.ReadFile(filepath.Join(runDir, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "shell-ok" {
		t.Fatalf("stdout = %q, want shell-ok", string(stdout))
	}
}

func TestRunIssueCancelledFromContext(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 9, Title: "Slow issue"}}
	fctx := testRunContext(t, repoRoot)
	c, cancel := context.WithCancel(context.Background())
	cancel()
	ctx := famctx.NewContext(c, fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      9,
		target:     "sleep:2s",
		captureDir: outDir,
	})
	if err == nil {
		t.Fatalf("runIssue succeeded unexpectedly")
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.FailureClass != runStatusCancelled {
		t.Fatalf("FailureClass = %q, want %q", env.FailureClass, runStatusCancelled)
	}
}

func TestIssueURLFallbackFromRegistry(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 10}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      10,
		target:     "success",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	want := "http://gitea:3000/botfam/botfam/issues/10"
	if env.IssueURL != want {
		t.Fatalf("IssueURL = %q, want %q", env.IssueURL, want)
	}
}

func TestRunIssueInvalidSleepDuration(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 11, Title: "Bad target"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      11,
		target:     "sleep:not-a-duration",
		captureDir: outDir,
	})
	if err == nil {
		t.Fatalf("runIssue succeeded unexpectedly")
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.FailureClass != runStatusRunnerError {
		t.Fatalf("FailureClass = %q, want %q", env.FailureClass, runStatusRunnerError)
	}
}
