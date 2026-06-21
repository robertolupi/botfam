package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	giteasdk "gitea.dev/sdk"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
)

type fakeIssueClient struct {
	issue     *forge.Issue
	issues    map[int]*forge.Issue
	wikiPages map[string]*forge.WikiPage
	err       error
	wikiErr   error
}

func (f fakeIssueClient) GetIssue(_ context.Context, n int) (*forge.Issue, error) {
	if f.issues != nil {
		if issue, ok := f.issues[n]; ok {
			return issue, nil
		}
	}
	return f.issue, f.err
}

func (f fakeIssueClient) GetWikiPage(_ context.Context, name string) (*forge.WikiPage, error) {
	if f.wikiPages != nil {
		if page, ok := f.wikiPages[name]; ok {
			return page, nil
		}
	}
	return nil, f.wikiErr
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

func TestRunIssueSignalFailureRecordsSignal(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 13, Title: "Signal command"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      13,
		target:     "shell:kill -TERM $$",
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
	if env.Signal == "" {
		t.Fatalf("Signal is empty, want recorded signal")
	}
	if env.ExitCode < 0 {
		t.Fatalf("ExitCode = %d, want schema-valid non-negative code", env.ExitCode)
	}
}

func TestRunIssuePrintsArtifactDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 19, Title: "Print artifacts"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()
	var out bytes.Buffer

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      19,
		target:     "success",
		captureDir: outDir,
		output:     &out,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "run artifacts: ") {
		t.Fatalf("output missing artifact directory: %q", got)
	}
	if strings.Contains(got, "run.json") {
		t.Fatalf("non-verbose output should not list files: %q", got)
	}
}

func TestRunIssueVerbosePrintsArtifactFiles(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 20, Title: "Print artifact files"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()
	var out bytes.Buffer

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      20,
		target:     "success",
		captureDir: outDir,
		verbose:    true,
		output:     &out,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "run artifacts: ") {
		t.Fatalf("output missing artifact directory: %q", got)
	}
	if !strings.Contains(got, "run.json") || !strings.Contains(got, "prompt.md") {
		t.Fatalf("verbose output missing artifact files: %q", got)
	}
}

func TestRunPromptResolvesForgeEntities(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{
		issues: map[int]*forge.Issue{
			123: {
				Index:   123,
				Title:   "Resolved referenced issue",
				Body:    "Issue body",
				HTMLURL: "http://gitea:3000/botfam/botfam/issues/123",
			},
		},
		wikiPages: map[string]*forge.WikiPage{
			"Architecture": {
				WikiPage: giteasdk.WikiPage{
					Title:   "Architecture",
					HTMLURL: "http://gitea:3000/botfam/botfam/wiki/Architecture",
				},
			},
		},
	}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		prompt:     "Design how to approach #123 using [[Architecture]]",
		target:     "success",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if env.Goal.Prompt != "Design how to approach #123 using [[Architecture]]" {
		t.Fatalf("Goal.Prompt = %q", env.Goal.Prompt)
	}
	if len(env.Goal.Entities) != 2 {
		t.Fatalf("Goal.Entities len = %d, want 2: %#v", len(env.Goal.Entities), env.Goal.Entities)
	}
	if env.Goal.Entities[0].Kind != "issue" || env.Goal.Entities[0].Number != 123 || env.Goal.Entities[0].Name != "Resolved referenced issue" {
		t.Fatalf("issue entity not resolved: %#v", env.Goal.Entities[0])
	}
	if env.Goal.Entities[1].Kind != "wiki_page" || env.Goal.Entities[1].Name != "Architecture" || env.Goal.Entities[1].URL == "" {
		t.Fatalf("wiki entity not resolved: %#v", env.Goal.Entities[1])
	}
}

func TestRunPromptResolvesPullRequestEntity(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{
		issues: map[int]*forge.Issue{
			456: {
				Index:       456,
				Title:       "Review run spike",
				HTMLURL:     "http://gitea:3000/botfam/botfam/pulls/456",
				PullRequest: &giteasdk.PullRequestMeta{},
			},
		},
	}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		prompt:     "Review PR #456",
		target:     "success",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if len(env.Goal.Entities) != 1 {
		t.Fatalf("Goal.Entities len = %d, want 1: %#v", len(env.Goal.Entities), env.Goal.Entities)
	}
	if env.Goal.Entities[0].Kind != "pr" || env.Goal.Entities[0].Number != 456 {
		t.Fatalf("PR entity not resolved: %#v", env.Goal.Entities[0])
	}
}

func TestRunIssueHarnessCommandTarget(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 13, Title: "Harness command target"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      13,
		target:     "harness:printf harness-ok",
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
	if strings.TrimSpace(string(stdout)) != "harness-ok" {
		t.Fatalf("stdout = %q, want harness-ok", string(stdout))
	}
}

func TestRunIssueDefaultHarnessCommandForCodex(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   16,
		Title:   "Default harness invocation",
		Body:    "Implement something useful",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/16",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "codex")
	if err := os.WriteFile(fakeCmd, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      16,
		harness:    "codex",
		target:     "harness",
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
	if !strings.Contains(env.HarnessCmd, "codex") {
		t.Fatalf("HarnessCmd = %q, want to contain codex", env.HarnessCmd)
	}
	stdout, err := os.ReadFile(filepath.Join(runDir, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stdout), "Default harness invocation") {
		t.Fatalf("stdout missing prompt title: %q", string(stdout))
	}
}

func TestRunIssueAgentFlagAndPrompt(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   18,
		Title:   "Agent flag prompt",
		Body:    "Do a quick summary.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/18",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "claude")
	if err := os.WriteFile(fakeCmd, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      18,
		agent:      "claude",
		agentSet:   true,
		prompt:     "Summarize this issue",
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
	if !strings.Contains(env.HarnessCmd, "claude") {
		t.Fatalf("HarnessCmd = %q, want to contain claude", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "--output-format") || !strings.Contains(env.HarnessCmd, "stream-json") {
		t.Fatalf("HarnessCmd = %q, want stream-json output format args", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "--verbose") {
		t.Fatalf("HarnessCmd = %q, want verbose output mode for stream-json", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "--include-hook-events") {
		t.Fatalf("HarnessCmd = %q, want hook event capture flag", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "--include-partial-messages") {
		t.Fatalf("HarnessCmd = %q, want partial message capture flag", env.HarnessCmd)
	}
	if env.Harness != "claude" {
		t.Fatalf("Harness = %q, want claude", env.Harness)
	}

	stdout, err := os.ReadFile(filepath.Join(runDir, "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stdout), "Summarize this issue") {
		t.Fatalf("prompt.md missing user prompt: %q", string(stdout))
	}
	if !strings.Contains(string(stdout), "# Goal") {
		t.Fatalf("prompt.md missing goal header: %q", string(stdout))
	}
	if !strings.Contains(string(stdout), "Agent flag prompt") {
		t.Fatalf("prompt.md missing issue title: %q", string(stdout))
	}
	if !strings.Contains(string(stdout), "Do a quick summary.") {
		t.Fatalf("prompt.md missing issue body: %q", string(stdout))
	}
}

func TestRunIssueClaudeHarnessParsesStreamJSONTranscript(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   21,
		Title:   "Claude stream transcript",
		Body:    "Emit two JSON lines.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/21",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "claude")
	fakeScript := []byte(`#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}],"model":"claude-3"}}'
printf '%s\n' '{"type":"result","subtype":"success","duration_ms":1,"duration_api_ms":1,"is_error":false,"num_turns":1,"session_id":"s1"}'
`)
	if err := os.WriteFile(fakeCmd, fakeScript, 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      21,
		agent:      "claude",
		agentSet:   true,
		prompt:     "Summarize this issue",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	transcriptPath := filepath.Join(runDir, "transcript.jsonl")
	b, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("missing transcript.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("transcript lines = %d, want 2", len(lines))
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parse first transcript line: %v", err)
	}
	if got := first["type"]; got != "assistant" {
		t.Fatalf("first event type = %v, want assistant", got)
	}
}

func TestRunIssueCodexHarnessParsesStreamJSONTranscript(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   22,
		Title:   "Codex stream transcript",
		Body:    "Emit two JSON lines.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/22",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "codex")
	fakeScript := []byte(`#!/bin/sh
printf '%s\n' '{"type":"session_start","session_id":"s1","timestamp":"2026-06-18T00:00:00Z"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":1}'
`)
	if err := os.WriteFile(fakeCmd, fakeScript, 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      22,
		agent:      "codex",
		agentSet:   true,
		prompt:     "Summarize this issue",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if !strings.Contains(env.HarnessCmd, "codex") {
		t.Fatalf("HarnessCmd = %q, want to contain codex", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "--json") {
		t.Fatalf("HarnessCmd = %q, want codex --json", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "-C") || !strings.Contains(env.HarnessCmd, repoRoot) {
		t.Fatalf("HarnessCmd = %q, want codex worktree -C arg", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "-o") || !strings.Contains(env.HarnessCmd, "final.md") {
		t.Fatalf("HarnessCmd = %q, want codex final-message output arg", env.HarnessCmd)
	}
	transcriptPath := filepath.Join(runDir, "transcript.jsonl")
	b, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("missing transcript.jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 {
		t.Fatalf("transcript lines = %d, want 3", len(lines))
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("parse last transcript line: %v", err)
	}
	if got := last["type"]; got != "result" {
		t.Fatalf("last event type = %v, want result", got)
	}
}

func TestRunIssueCodexHarnessCapturesTokenUsage(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   23,
		Title:   "Codex usage",
		Body:    "Emit usage.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/23",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "codex")
	fakeScript := []byte(`#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    printf 'final answer\n' > "$1"
  fi
  shift
done
printf '%s\n' '{"type":"thread.started","thread_id":"s1"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"ok"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":7,"reasoning_output_tokens":3}}'
`)
	if err := os.WriteFile(fakeCmd, fakeScript, 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      23,
		agent:      "codex",
		agentSet:   true,
		prompt:     "Summarize this issue",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if got := env.TokenUsage["source"]; got != "stream_event" {
		t.Fatalf("TokenUsage source = %v, want stream_event: %#v", got, env.TokenUsage)
	}
	if got := env.TokenUsage["input_tokens"]; got != float64(100) {
		t.Fatalf("input_tokens = %v, want 100: %#v", got, env.TokenUsage)
	}
	// total_tokens follows the session-metric convention: input+output+cache
	// (100+7+40); reasoning_output_tokens is excluded as a subset of output.
	if got := env.TokenUsage["total_tokens"]; got != float64(147) {
		t.Fatalf("total_tokens = %v, want 147: %#v", got, env.TokenUsage)
	}
	final, err := os.ReadFile(filepath.Join(runDir, "final.md"))
	if err != nil {
		t.Fatalf("missing final.md: %v", err)
	}
	if strings.TrimSpace(string(final)) != "final answer" {
		t.Fatalf("final.md = %q", string(final))
	}
}

func TestRunIssueClaudeHarnessCapturesTokenUsage(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   25,
		Title:   "Claude usage",
		Body:    "Emit usage.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/25",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "claude")
	// Claude Code's final result event carries an Anthropic-API-shaped usage
	// object plus a running total_cost_usd.
	fakeScript := []byte(`#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}],"model":"claude-opus-4"}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"num_turns":2,"total_cost_usd":0.0123,"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":40,"output_tokens":7}}'
`)
	if err := os.WriteFile(fakeCmd, fakeScript, 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      25,
		agent:      "claude",
		agentSet:   true,
		prompt:     "Summarize this issue",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if got := env.TokenUsage["source"]; got != "stream_event" {
		t.Fatalf("TokenUsage source = %v, want stream_event: %#v", got, env.TokenUsage)
	}
	// Shared envelope keys — parity with the Codex extractor (numbers come back
	// as float64 after the run.json round-trip).
	for key, want := range map[string]float64{
		"input_tokens":        100,
		"output_tokens":       7,
		"cached_input_tokens": 40,  // mapped from Claude's cache_read_input_tokens
		"total_tokens":        167, // input+output+cache_read+cache_creation (100+7+40+20)
	} {
		if got := env.TokenUsage[key]; got != want {
			t.Fatalf("TokenUsage[%q] = %v, want %v: %#v", key, got, want, env.TokenUsage)
		}
	}
	// Richer-than-Codex fields: cache-creation tokens and dollar cost.
	if got := env.TokenUsage["cache_creation_input_tokens"]; got != float64(20) {
		t.Fatalf("cache_creation_input_tokens = %v, want 20: %#v", got, env.TokenUsage)
	}
	if got := env.TokenUsage["total_cost_usd"]; got != float64(0.0123) {
		t.Fatalf("total_cost_usd = %v, want 0.0123: %#v", got, env.TokenUsage)
	}
}

func TestRunIssueAntigravityHarnessCapturesConversation(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   24,
		Title:   "Antigravity conversation capture",
		Body:    "Say hello in one word.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/24",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	// Stand up a fake Antigravity app-data dir with a per-conversation store so
	// the artifact annotator can resolve and stat the trajectory DB.
	appData := t.TempDir()
	convID := "99f89a01-243b-4601-ab35-aa5f2dd15d59"
	if err := os.MkdirAll(filepath.Join(appData, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(appData, "conversations", convID+".db")
	if err := os.WriteFile(dbPath, []byte("fake-sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "agy")
	// The fake agy parses --log-file (which must precede --print), writes a
	// glog-style log naming the app-data dir and conversation id, then prints a
	// rendered answer (agy has no JSON output mode).
	replacer := strings.NewReplacer("APPDATA", appData, "CONVID", convID)
	fakeScript := replacer.Replace(`#!/bin/sh
logfile=""
while [ $# -gt 0 ]; do
  case "$1" in
    --log-file) logfile="$2"; shift 2;;
    --print) shift 2;;
    *) shift;;
  esac
done
if [ -n "$logfile" ]; then
  echo "I0621 server.go:214] Creating CLI server backend: appDataDir=APPDATA cascadeManager=true" > "$logfile"
  echo "I0621 server.go:789] Created conversation CONVID" >> "$logfile"
fi
echo Hello
`)
	if err := os.WriteFile(fakeCmd, []byte(fakeScript), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      24,
		agent:      "antigravity",
		agentSet:   true,
		prompt:     "Say hello in one word",
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
	// --log-file must come before --print so agy does not treat it as the prompt.
	if !strings.Contains(env.HarnessCmd, "agy") {
		t.Fatalf("HarnessCmd = %q, want to contain agy", env.HarnessCmd)
	}
	logIdx := strings.Index(env.HarnessCmd, "--log-file")
	printIdx := strings.Index(env.HarnessCmd, "--print")
	if logIdx < 0 || printIdx < 0 || logIdx > printIdx {
		t.Fatalf("HarnessCmd = %q, want --log-file before --print", env.HarnessCmd)
	}
	// Token usage is unavailable from the CLI surface.
	if got := env.TokenUsage["source"]; got != "unavailable" {
		t.Fatalf("TokenUsage source = %v, want unavailable: %#v", got, env.TokenUsage)
	}

	// The diagnostic log is captured as a durable artifact.
	if _, err := os.Stat(filepath.Join(runDir, "agy-cli.log")); err != nil {
		t.Fatalf("missing agy-cli.log: %v", err)
	}
	artBytes, err := os.ReadFile(filepath.Join(runDir, "artifacts.json"))
	if err != nil {
		t.Fatalf("read artifacts.json: %v", err)
	}
	var artifacts map[string]any
	if err := json.Unmarshal(artBytes, &artifacts); err != nil {
		t.Fatalf("parse artifacts.json: %v", err)
	}
	if got := artifacts["conversation_id"]; got != convID {
		t.Fatalf("artifacts conversation_id = %v, want %q", got, convID)
	}
	if got := artifacts["conversation_db"]; got != dbPath {
		t.Fatalf("artifacts conversation_db = %v, want %q", got, dbPath)
	}
}

func TestValidatePermissionMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "default", false},
		{"  ", "default", false},
		{"default", "default", false},
		{"auto", "auto", false},
		{"bypass", "bypass", false},
		{"Bypass", "", true},      // case-sensitive
		{"yolo", "", true},        // unknown
		{"acceptEdits", "", true}, // harness-native value is not a canonical mode
	} {
		got, err := validatePermissionMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("validatePermissionMode(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("validatePermissionMode(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("validatePermissionMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHarnessPermissionArgs(t *testing.T) {
	for _, tc := range []struct {
		harness string
		mode    string
		want    []string
	}{
		{famconfig.HarnessClaudeCode, "default", nil},
		{famconfig.HarnessClaudeCode, "auto", []string{"--permission-mode", "acceptEdits"}},
		{famconfig.HarnessClaudeCode, "bypass", []string{"--permission-mode", "bypassPermissions"}},
		{famconfig.HarnessCodex, "default", nil},
		{famconfig.HarnessCodex, "auto", []string{"--sandbox", "workspace-write"}},
		{famconfig.HarnessCodex, "bypass", []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{famconfig.HarnessAntigravity, "default", nil},
		{famconfig.HarnessAntigravity, "auto", []string{"--dangerously-skip-permissions"}},
		{famconfig.HarnessAntigravity, "bypass", []string{"--dangerously-skip-permissions"}},
	} {
		got := harnessPermissionArgs(tc.harness, tc.mode)
		if strings.Join(got, " ") != strings.Join(tc.want, " ") {
			t.Errorf("harnessPermissionArgs(%s,%s) = %v, want %v", tc.harness, tc.mode, got, tc.want)
		}
	}
}

func TestRunIssuePermissionModeReachesHarnessCommand(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 26, Title: "Permission mode"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte("#!/bin/sh\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	t.Run("bypass adds the flag", func(t *testing.T) {
		outDir := t.TempDir()
		err := runIssue(ctx, client, fctx, runOptions{
			issue: 26, agent: "claude", agentSet: true, target: "harness",
			permissionMode: "bypass", captureDir: outDir,
		})
		if err != nil {
			t.Fatalf("runIssue: %v", err)
		}
		env := mustReadRunEnvelope(t, findRunDir(t, outDir))
		if !strings.Contains(env.HarnessCmd, "--permission-mode") || !strings.Contains(env.HarnessCmd, "bypassPermissions") {
			t.Fatalf("HarnessCmd = %q, want --permission-mode bypassPermissions", env.HarnessCmd)
		}
	})

	t.Run("default adds nothing", func(t *testing.T) {
		outDir := t.TempDir()
		err := runIssue(ctx, client, fctx, runOptions{
			issue: 26, agent: "claude", agentSet: true, target: "harness",
			permissionMode: "default", captureDir: outDir,
		})
		if err != nil {
			t.Fatalf("runIssue: %v", err)
		}
		env := mustReadRunEnvelope(t, findRunDir(t, outDir))
		if strings.Contains(env.HarnessCmd, "--permission-mode") {
			t.Fatalf("HarnessCmd = %q, should not contain --permission-mode in default mode", env.HarnessCmd)
		}
	})
}

func TestParseAllowTools(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"mcp__botfam", []string{"mcp__botfam"}},
		{"mcp__botfam ToolSearch", []string{"mcp__botfam", "ToolSearch"}},
		{"mcp__botfam,ToolSearch", []string{"mcp__botfam", "ToolSearch"}},
		{"mcp__botfam, ToolSearch ", []string{"mcp__botfam", "ToolSearch"}},
	} {
		got := parseAllowTools(tc.in)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("parseAllowTools(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateAllowTools(t *testing.T) {
	if err := validateAllowTools("codex", ""); err != nil {
		t.Errorf("empty allow-tools should be fine on any harness: %v", err)
	}
	if err := validateAllowTools("claude", "mcp__botfam"); err != nil {
		t.Errorf("claude allow-tools should be accepted: %v", err)
	}
	if err := validateAllowTools("codex", "mcp__botfam"); err != nil {
		t.Errorf("codex allow-tools should be accepted: %v", err)
	}
	if err := validateAllowTools("antigravity", "mcp__botfam"); err == nil {
		t.Errorf("allow-tools on antigravity should be rejected")
	}
}

func TestDefaultRunOptionsUsesConfigRunDefaults(t *testing.T) {
	fctx := famctx.Context{
		Registry: famconfig.Registry{
			Run: famconfig.RunConfig{
				PermissionMode: "auto",
				AllowTools:     []string{"mcp__botfam", "ToolSearch"},
			},
		},
		Agent: famconfig.AgentConfig{
			Run: famconfig.RunConfig{
				PermissionMode: "bypass",
				AllowTools:     []string{"mcp__botfam__forge_pull_request_read"},
			},
		},
	}
	got := defaultRunOptions(runOptions{}, fctx)
	if got.permissionMode != "bypass" {
		t.Fatalf("permissionMode = %q, want bypass", got.permissionMode)
	}
	if got.allowTools != "mcp__botfam__forge_pull_request_read" {
		t.Fatalf("allowTools = %q", got.allowTools)
	}

	explicit := defaultRunOptions(runOptions{permissionMode: "default", allowTools: "mcp__gopls"}, fctx)
	if explicit.permissionMode != "default" || explicit.allowTools != "mcp__gopls" {
		t.Fatalf("explicit options should win over config, got %+v", explicit)
	}
}

func TestRunIssueAllowToolsReachesHarnessCommand(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 27, Title: "Allow tools"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte("#!/bin/sh\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	outDir := t.TempDir()
	err := runIssue(ctx, client, fctx, runOptions{
		issue: 27, agent: "claude", agentSet: true, target: "harness",
		allowTools: "mcp__botfam ToolSearch", captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	env := mustReadRunEnvelope(t, findRunDir(t, outDir))
	for _, want := range []string{"--allowedTools", "mcp__botfam", "ToolSearch"} {
		if !strings.Contains(env.HarnessCmd, want) {
			t.Fatalf("HarnessCmd = %q, want to contain %q", env.HarnessCmd, want)
		}
	}
	// Scoped allowlist must NOT escalate to full bypass.
	if strings.Contains(env.HarnessCmd, "bypassPermissions") {
		t.Fatalf("HarnessCmd = %q, allow-tools should not imply bypass", env.HarnessCmd)
	}
}

func TestRunIssueAllowToolsFromConfigReachesClaudeCommand(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 28, Title: "Configured allow tools"}}
	fctx := testRunContext(t, repoRoot)
	fctx.Registry.Run = famconfig.RunConfig{
		PermissionMode: "auto",
		AllowTools:     []string{"mcp__botfam", "ToolSearch"},
	}
	ctx := famctx.NewContext(context.Background(), fctx)

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte("#!/bin/sh\nprintf 'ok\\n'\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	outDir := t.TempDir()
	err := runIssue(ctx, client, fctx, runOptions{
		issue: 28, agent: "claude", agentSet: true, target: "harness",
		captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	env := mustReadRunEnvelope(t, findRunDir(t, outDir))
	for _, want := range []string{"--permission-mode", "acceptEdits", "--allowedTools", "mcp__botfam", "ToolSearch"} {
		if !strings.Contains(env.HarnessCmd, want) {
			t.Fatalf("HarnessCmd = %q, want to contain %q", env.HarnessCmd, want)
		}
	}
}

func TestCodexAllowToolsConfigArgs(t *testing.T) {
	got := codexAllowToolsConfigArgs("ToolSearch mcp__botfam__forge_pull_request_read mcp__botfam__forge_issue_read mcp__gopls")
	joined := strings.Join(got, " ")
	for _, want := range []string{
		`mcp_servers.botfam.default_tools_approval_mode="approve"`,
		`mcp_servers.botfam.enabled_tools=["forge_issue_read","forge_pull_request_read"]`,
		`mcp_servers.botfam.tools.forge_issue_read.approval_mode="approve"`,
		`mcp_servers.botfam.tools.forge_pull_request_read.approval_mode="approve"`,
		`mcp_servers.gopls.default_tools_approval_mode="approve"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex config args = %v, want %s", got, want)
		}
	}
	if strings.Contains(joined, "ToolSearch") {
		t.Fatalf("non-MCP tool should not be translated to Codex MCP config: %v", got)
	}
}

func TestRunIssueCodexAllowToolsReachesConfigArgs(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 29, Title: "Codex allow tools"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)

	outDir := t.TempDir()
	err := runIssue(ctx, client, fctx, runOptions{
		issue: 29, agent: "codex", agentSet: true, target: "harness",
		allowTools: "mcp__botfam__forge_pull_request_read ToolSearch", captureDir: outDir,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}
	runDir := findRunDir(t, outDir)
	stdout, err := os.ReadFile(filepath.Join(runDir, "stdout.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(stdout)
	for _, want := range []string{
		"-c",
		`mcp_servers.botfam.default_tools_approval_mode="approve"`,
		`mcp_servers.botfam.enabled_tools=["forge_pull_request_read"]`,
		`mcp_servers.botfam.tools.forge_pull_request_read.approval_mode="approve"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want to contain %q", got, want)
		}
	}
	if strings.Contains(got, "ToolSearch") {
		t.Fatalf("stdout = %q, non-MCP ToolSearch should not become Codex config", got)
	}
}

func TestRunIssueCodexHarnessPassesOTELEndpoint(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{
		Index:   24,
		Title:   "Codex otel",
		Body:    "Emit traces.",
		HTMLURL: "http://gitea:3000/botfam/botfam/issues/24",
	}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()
	tracePath := filepath.Join(t.TempDir(), "traces.jsonl")
	if err := os.WriteFile(tracePath, []byte("old trace batch\n"), 0o644); err != nil {
		t.Fatalf("write trace seed: %v", err)
	}

	oldPath := os.Getenv("PATH")
	fakeBin := t.TempDir()
	fakeCmd := filepath.Join(fakeBin, "codex")
	fakeScript := []byte(`#!/bin/sh
printf '%s\n' "$*"
printf '%s\n' '{"resourceSpans":[{"scopeSpans":[]}]} ' >> "$BOTFAM_TEST_OTEL_TRACE_FILE"
`)
	if err := os.WriteFile(fakeCmd, fakeScript, 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("PATH", fakeBin+":"+oldPath)
	t.Setenv("BOTFAM_TEST_OTEL_TRACE_FILE", tracePath)

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      24,
		agent:      "codex",
		agentSet:   true,
		prompt:     "Summarize this issue",
		captureDir: outDir,
		otel:       "http://127.0.0.1:4318/v1/traces",
		otelTraces: tracePath,
	})
	if err != nil {
		t.Fatalf("runIssue: %v", err)
	}

	runDir := findRunDir(t, outDir)
	env := mustReadRunEnvelope(t, runDir)
	if !strings.Contains(env.HarnessCmd, "otel.trace_exporter") {
		t.Fatalf("HarnessCmd = %q, want otel trace exporter override", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, "http://127.0.0.1:4318/v1/traces") {
		t.Fatalf("HarnessCmd = %q, want otel endpoint", env.HarnessCmd)
	}
	if !strings.Contains(env.HarnessCmd, `otel.metrics_exporter=\"none\"`) {
		t.Fatalf("HarnessCmd = %q, want local run to disable default metrics exporter", env.HarnessCmd)
	}
	traceArtifact, err := os.ReadFile(filepath.Join(runDir, "otel-traces.jsonl"))
	if err != nil {
		t.Fatalf("missing otel-traces.jsonl: %v", err)
	}
	if got := string(traceArtifact); strings.Contains(got, "old trace batch") || !strings.Contains(got, "resourceSpans") {
		t.Fatalf("otel-traces.jsonl should contain only new trace data, got %q", got)
	}
}

func TestRunIssueHarnessCommandFromEnv(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 14, Title: "Harness env command"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	t.Setenv("BOTFAM_RUN_HARNESS_CMD", "printf env-ok")

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      14,
		target:     "harness",
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
	if strings.TrimSpace(string(stdout)) != "env-ok" {
		t.Fatalf("stdout = %q, want env-ok", string(stdout))
	}
}

func TestRunIssueHarnessMissingCommand(t *testing.T) {
	repoRoot := t.TempDir()
	initGitRepo(t, repoRoot)
	client := fakeIssueClient{issue: &forge.Issue{Index: 15, Title: "Harness missing command"}}
	fctx := testRunContext(t, repoRoot)
	ctx := famctx.NewContext(context.Background(), fctx)
	outDir := t.TempDir()

	// Ensure env fallback is not set and no command flag is provided.
	t.Setenv("BOTFAM_RUN_HARNESS_CMD", "")

	err := runIssue(ctx, client, fctx, runOptions{
		issue:      15,
		harness:    "bogus-harness",
		target:     "harness",
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

func TestRunBashOllamaCommand(t *testing.T) {
	t.Run("uses_default_prompt", func(t *testing.T) {
		got := runBashOllamaCommand("ollama")
		want := `ollama run --think=false gpt-oss:20b "Hello. Tell me a joke about bananas."`
		if got != want {
			t.Fatalf("runBashOllamaCommand(ollama) = %q, want %q", got, want)
		}
	})

	t.Run("uses_explicit_prompt", func(t *testing.T) {
		got := runBashOllamaCommand("ollama:Tell me a short pun about pears")
		want := `ollama run --think=false gpt-oss:20b "Tell me a short pun about pears"`
		if got != want {
			t.Fatalf("runBashOllamaCommand(explicit) = %q, want %q", got, want)
		}
	})

	t.Run("trims_empty_prompt_back_to_default", func(t *testing.T) {
		got := runBashOllamaCommand("ollama:   ")
		want := `ollama run --think=false gpt-oss:20b "Hello. Tell me a joke about bananas."`
		if got != want {
			t.Fatalf("runBashOllamaCommand(empty) = %q, want %q", got, want)
		}
	})
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
