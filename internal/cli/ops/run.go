package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

type runOptions struct {
	issue      int
	harness    string
	target     string
	harnessCmd string
	timeoutS   int
	captureDir string
}

const runCommandHelp = `botfam run --issue <number> executes one issue through a harness.

This spike implements a durable session envelope + process lifecycle tracing. It
writes the required artifacts:

The run.json schema lives in doc/run-issue-session-capture.schema.json.

Default target is 'success', which executes 'bash -lc env' as a baseline command.
Use --target "shell:<command>" to run a custom shell command.
Use --target "ollama:<prompt>" to run an Ollama command with gpt-oss:20b.
Use --target "ollama" for a canned demonstration prompt.
Use --target "harness[:<command>]" to run a harness command (from --harness-command or BOTFAM_RUN_HARNESS_CMD).
  run.json
  prompt.md
  stdout.log
  stderr.log
  (optional when produced) transcript.jsonl
  (optional when produced) artifacts.json
  (optional when produced) env.redacted.json
`

const (
	runStatusSuccess       = "success"
	runStatusFailed        = "failed"
	runStatusTimeout       = "timeout"
	runStatusCancelled     = "cancelled"
	runStatusToolError     = "tool_error"
	runStatusRunnerError   = "runner_error"
	runStatusUnknown       = "unknown"
	runDefaultHarness      = "codex"
	runTargetSuccessPrefix = "success"
	runTargetOllamaPrefix  = "ollama"
	runTargetHarnessPrefix = "harness"
	runHarnessCmdEnvVar    = "BOTFAM_RUN_HARNESS_CMD"
)

type issueClient interface {
	GetIssue(ctx context.Context, num int) (*forge.Issue, error)
}

type runFailureClass string

type harnessResult struct {
	Status      runFailureClass
	ExitCode    int
	CommandLine string
	Stdout      string
	Stderr      string
	Transcript  []map[string]any
	Artifacts   map[string]any
}

type runEnvelope struct {
	RunID          string         `json:"run_id"`
	StartAt        string         `json:"start_at"`
	EndAt          string         `json:"end_at"`
	DurationMs     int64          `json:"duration_ms"`
	IssueNumber    int            `json:"issue_number"`
	IssueURL       string         `json:"issue_url"`
	IssueTitle     string         `json:"issue_title"`
	Harness        string         `json:"harness"`
	HarnessCmd     string         `json:"harness_command"`
	Target         string         `json:"target"`
	WorktreePath   string         `json:"worktree_path"`
	Branch         string         `json:"branch"`
	FailureClass   string         `json:"failure_class"`
	ExitCode       int            `json:"exit_code"`
	Signal         string         `json:"signal,omitempty"`
	ForgeArtifacts map[string]any `json:"forge_artifacts"`
	TokenUsage     map[string]any `json:"token_usage"`
}

// NewRunCmd builds `botfam run`.
func NewRunCmd() *cobra.Command {
	var opts runOptions
	c := &cobra.Command{
		Use:           "run",
		Short:         "Run one issue through a harness (prototype)",
		Long:          runCommandHelp,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: cmdutil.RunWithRegistryCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
			ro := opts
			if ro.issue <= 0 {
				return fmt.Errorf("--issue is required and must be >= 1")
			}
			if ro.timeoutS < 0 {
				return fmt.Errorf("--timeout must be >= 0")
			}
			if ro.harness == "" {
				fctx, ok := famctx.FromContext(ctx)
				if ok && fctx.Harness != "" {
					ro.harness = fctx.Harness
				}
			}
			if ro.harness == "" {
				ro.harness = runDefaultHarness
			}
			if ro.target == "" {
				ro.target = runTargetSuccessPrefix
			}

			fctx, ok := famctx.FromContext(ctx)
			if !ok {
				return fmt.Errorf("run: missing family context")
			}
			client, err := forge.NewClient(ctx)
			if err != nil {
				return err
			}
			return runIssue(ctx, client, fctx, ro)
		}),
	}
	flags := c.Flags()
	flags.IntVar(&opts.issue, "issue", 0, "forge issue/PR number to run")
	flags.StringVar(&opts.harness, "harness", "", "harness override (default: detected harness, then codex)")
	flags.StringVar(&opts.target, "target", "", "harness target for fake mode (e.g. success, fail, sleep:2s)")
	flags.StringVar(&opts.harnessCmd, "harness-command", "", "command for --target harness (or set BOTFAM_RUN_HARNESS_CMD)")
	flags.IntVar(&opts.timeoutS, "timeout", 0, "wall-clock timeout in seconds (0 = no timeout)")
	flags.StringVar(&opts.captureDir, "capture-dir", "", "capture directory (default: $FAMDIR/runs)")
	return c
}

func runIssue(ctx context.Context, client issueClient, fctx famctx.Context, opts runOptions) error {
	issue, err := client.GetIssue(ctx, opts.issue)
	if err != nil {
		return fmt.Errorf("run: fetch issue %d: %w", opts.issue, err)
	}

	captureRoot := opts.captureDir
	if captureRoot == "" {
		captureRoot = filepath.Join(fctx.FamDir, "runs")
	}
	runID := makeRunID(opts.issue, opts.harness)
	runDir := filepath.Join(captureRoot, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("run: create run directory %s: %w", runDir, err)
	}

	timeout := time.Duration(opts.timeoutS) * time.Second
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	branch, err := branchName(fctx)
	if err != nil {
		branch = "unknown"
	}

	start := time.Now().UTC()
	hResult := runFakeHarness(runCtx, opts.harness, opts.target, opts.harnessCmd, issue)
	end := time.Now().UTC()
	status := hResult.Status
	if status == runStatusSuccess && runCtx.Err() != nil {
		status = runStatusTimeout
		if runCtx.Err() == context.Canceled {
			status = runStatusCancelled
		}
	}

	runEnv, runRepo := resolveIssueInfo(fctx.Registry)
	if runRepo == "" && issue.Repository != nil && issue.Repository.FullName != "" {
		runRepo = issue.Repository.FullName
	}
	issueURL := issueURL(issue, runEnv, runRepo, opts.issue)

	artifact := runEnvelope{
		RunID:        runID,
		StartAt:      start.Format(time.RFC3339Nano),
		EndAt:        end.Format(time.RFC3339Nano),
		DurationMs:   end.Sub(start).Milliseconds(),
		IssueNumber:  int(issue.Index),
		IssueURL:     issueURL,
		IssueTitle:   issue.Title,
		Harness:      opts.harness,
		HarnessCmd:   hResult.CommandLine,
		Target:       opts.target,
		WorktreePath: fctx.WorktreeRoot,
		Branch:       branch,
		FailureClass: string(status),
		ExitCode:     hResult.ExitCode,
		ForgeArtifacts: map[string]any{
			"comments":      []string{},
			"issues":        []string{},
			"pull_requests": []string{},
		},
		TokenUsage: map[string]any{
			"source": "unavailable",
		},
	}

	if status == "" {
		status = runFailureClass(runStatusUnknown)
		artifact.FailureClass = string(status)
	}

	stdoutPath := filepath.Join(runDir, "stdout.log")
	stderrPath := filepath.Join(runDir, "stderr.log")
	if err := os.WriteFile(stdoutPath, []byte(hResult.Stdout), 0o644); err != nil {
		return fmt.Errorf("run: write stdout log: %w", err)
	}
	if err := os.WriteFile(stderrPath, []byte(hResult.Stderr), 0o644); err != nil {
		return fmt.Errorf("run: write stderr log: %w", err)
	}

	if err := writeJSON(filepath.Join(runDir, "run.json"), artifact); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte(makePrompt(issue, issueURL)), 0o644); err != nil {
		return fmt.Errorf("run: write prompt.md: %w", err)
	}
	if hResult.Transcript != nil {
		if err := writeJSONL(filepath.Join(runDir, "transcript.jsonl"), hResult.Transcript); err != nil {
			return fmt.Errorf("run: write transcript.jsonl: %w", err)
		}
	}
	if hResult.Artifacts != nil {
		if err := writeJSON(filepath.Join(runDir, "artifacts.json"), hResult.Artifacts); err != nil {
			return fmt.Errorf("run: write artifacts.json: %w", err)
		}
	} else {
		if err := writeJSON(filepath.Join(runDir, "artifacts.json"), map[string]any{}); err != nil {
			return fmt.Errorf("run: write artifacts.json: %w", err)
		}
	}
	redactedEnv := map[string]any{
		"run_id":       runID,
		"issue_number": int(issue.Index),
		"harness":      opts.harness,
		"target":       opts.target,
		"working_tree": fctx.WorktreeRoot,
		"repository":   runRepo,
		"status":       string(status),
	}
	if err := writeJSON(filepath.Join(runDir, "env.redacted.json"), redactedEnv); err != nil {
		return fmt.Errorf("run: write env.redacted.json: %w", err)
	}

	if status != runStatusSuccess {
		return fmt.Errorf("run %s finished with status %s", runID, status)
	}
	return nil
}

func runFakeHarness(ctx context.Context, harness, target, harnessCommand string, issue *forge.Issue) harnessResult {
	cmd := fmt.Sprintf("fake-harness --harness=%s --target=%s --issue=%d", harness, target, issue.Index)
	target = strings.ToLower(strings.TrimSpace(target))
	switch {
	case strings.HasPrefix(target, "fail"):
		return harnessResult{
			Status:      runStatusToolError,
			ExitCode:    1,
			CommandLine: cmd,
			Stdout:      "",
			Stderr:      "fake harness failure\n",
			Artifacts: map[string]any{
				"status": "failed",
			},
		}
	case strings.HasPrefix(target, "sleep:"):
		raw := strings.TrimPrefix(target, "sleep:")
		d, err := time.ParseDuration(raw)
		if err != nil {
			return harnessResult{
				Status:      runStatusRunnerError,
				ExitCode:    2,
				CommandLine: cmd,
				Stderr:      "fake harness invalid sleep duration: " + raw + "\n",
			}
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			return harnessResult{
				Status:      runStatusSuccess,
				ExitCode:    0,
				CommandLine: cmd,
				Stdout:      "fake harness slept " + raw + "\n",
				Stderr:      "",
				Transcript: []map[string]any{
					{"event": "sleep", "duration": raw},
					{"event": "target", "issue": issue.Index},
				},
			}
		case <-ctx.Done():
			status := runFailureClass(runStatusTimeout)
			if ctx.Err() == context.Canceled {
				status = runFailureClass(runStatusCancelled)
			}
			return harnessResult{
				Status:      status,
				ExitCode:    124,
				CommandLine: cmd,
				Stderr:      "fake harness " + string(status) + "\n",
				Transcript: []map[string]any{
					{"event": "sleep", "duration": raw, "status": string(status)},
					{"event": "target", "issue": issue.Index},
				},
			}
		}
	case target == runTargetSuccessPrefix || target == "env":
		return runBashHarness(ctx, "env", cmd, issue.Index)
	case strings.HasPrefix(target, "shell:"):
		script := strings.TrimSpace(strings.TrimPrefix(target, "shell:"))
		if script == "" {
			script = "env"
		}
		return runBashHarness(ctx, script, cmd, issue.Index)
	case target == runTargetOllamaPrefix || strings.HasPrefix(target, runTargetOllamaPrefix+":"):
		return runBashHarness(ctx, runBashOllamaCommand(target), cmd, issue.Index)
	case target == runTargetHarnessPrefix || strings.HasPrefix(target, runTargetHarnessPrefix+":"):
		command := ""
		if strings.HasPrefix(target, runTargetHarnessPrefix+":") {
			command = strings.TrimSpace(strings.TrimPrefix(target, runTargetHarnessPrefix+":"))
		}
		resolved, ok := resolveHarnessCommand(command, harnessCommand)
		if !ok {
			return harnessResult{
				Status:      runStatusRunnerError,
				ExitCode:    3,
				CommandLine: cmd,
				Stderr:      "run: no harness command configured; use --harness-command or BOTFAM_RUN_HARNESS_CMD or --target harness:<command>\n",
			}
		}
		return runBashHarness(ctx, resolved, resolved, issue.Index)
	default:
		return runBashHarness(ctx, "env", cmd, issue.Index)
	}
}

func resolveHarnessCommand(inline, fallback string) (string, bool) {
	if inline != "" {
		return inline, true
	}
	if fallback != "" {
		return strings.TrimSpace(fallback), true
	}
	if envCmd := strings.TrimSpace(os.Getenv(runHarnessCmdEnvVar)); envCmd != "" {
		return envCmd, true
	}
	return "", false
}

func runBashOllamaCommand(target string) string {
	prompt := "Hello. Tell me a joke about bananas."
	if strings.HasPrefix(target, runTargetOllamaPrefix+":") {
		prompt = strings.TrimSpace(strings.TrimPrefix(target, runTargetOllamaPrefix+":"))
		if prompt == "" {
			prompt = "Hello. Tell me a joke about bananas."
		}
	}
	return fmt.Sprintf("ollama run --think=false gpt-oss:20b %s", strconv.Quote(prompt))
}

func runBashHarness(ctx context.Context, shellCommand, commandLine string, issue int64) harnessResult {
	bash := exec.CommandContext(ctx, "bash", "-lc", shellCommand)
	var stdout, stderr bytes.Buffer
	bash.Stdout = &stdout
	bash.Stderr = &stderr
	err := bash.Run()

	status := runFailureClass(runStatusSuccess)
	exitCode := 0
	if err != nil {
		status = runFailureClass(runStatusToolError)
		exitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			status = runFailureClass(runStatusTimeout)
			exitCode = 124
		}
		if ctx.Err() == context.Canceled {
			status = runFailureClass(runStatusCancelled)
			exitCode = 124
		}
	}

	return harnessResult{
		Status:      status,
		ExitCode:    exitCode,
		CommandLine: commandLine,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		Transcript: []map[string]any{
			{"event": "shell", "command": shellCommand},
			{"event": "issue", "index": issue},
		},
		Artifacts: map[string]any{
			"command": shellCommand,
		},
	}
}

func makeRunID(issue int, harness string) string {
	return fmt.Sprintf("run-%s-%s-%d", time.Now().UTC().Format("20060102T150405.000000000"), sanitizeRunToken(harness), issue)
}

func sanitizeRunToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "harness"
	}
	var b strings.Builder
	for _, ch := range s {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			b.WriteRune(ch + ('a' - 'A'))
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '-':
			b.WriteRune(ch)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return "harness"
	}
	return out
}

func branchName(fctx famctx.Context) (string, error) {
	if fctx.WorktreeRoot == "" {
		return "", fmt.Errorf("run: empty worktree path")
	}
	return fctx.CurrentBranch()
}

func issueURL(issue *forge.Issue, runEnv, runRepo string, num int) string {
	if issue != nil {
		if issue.HTMLURL != "" {
			return issue.HTMLURL
		}
		if issue.URL != "" {
			return issue.URL
		}
	}
	return fmt.Sprintf("%s/%s/issues/%d", runEnv, runRepo, num)
}

func resolveIssueInfo(reg famconfig.Registry) (base, repo string) {
	base = strings.TrimSuffix(reg.ForgeURL, "/")
	repo = reg.Repository
	if base == "" {
		base = "http://localhost"
	}
	return base, repo
}

func makePrompt(issue *forge.Issue, issueURL string) string {
	return fmt.Sprintf("# Issue %d\n\nURL: %s\n\nTitle: %s\n\n%s\n", issue.Index, issueURL, issue.Title, issue.Body)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeJSONL(path string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	var b []byte
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			return err
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	return os.WriteFile(path, b, 0o644)
}
