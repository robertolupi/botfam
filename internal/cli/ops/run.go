package ops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/robertolupi/botfam/internal/cli/cmdutil"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/spf13/cobra"
)

type runOptions struct {
	issue      int
	agent      string
	agentSet   bool
	harness    string
	target     string
	prompt     string
	harnessCmd string
	verbose    bool
	otel       string
	otelTraces string
	timeoutS   int
	captureDir string
	output     io.Writer
}

const runCommandHelp = `botfam run executes one goal prompt through an agent.

This spike implements a durable session envelope + process lifecycle tracing. It
writes the required artifacts:

The run.json schema lives in doc/run-issue-session-capture.schema.json.

Use --prompt to provide the goal. --issue <number> is shorthand that resolves
the issue and builds a goal prompt from it.

Default target is 'success', which executes 'bash -lc env' as a baseline command.
If --agent is explicitly provided and --target is omitted, target defaults to 'harness'.
Use --target "shell:<command>" to run a custom shell command.
Use --target "ollama:<prompt>" to run an Ollama command with gpt-oss:20b.
Use --target "ollama" for a canned demonstration prompt.
Use --target "harness[:<command>]" to run an agent command (from --harness-command or BOTFAM_RUN_HARNESS_CMD).
  run.json
  prompt.md
  stdout.log
	stderr.log
	(optional when produced) transcript.jsonl
	(optional when produced) final.md
	(optional when produced) otel-traces.jsonl
	(optional when produced) artifacts.json
  (optional when produced) env.redacted.json
`

const (
	runStatusSuccess       = "success"
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
	runOTELEndpointEnvVar  = "BOTFAM_RUN_OTEL_ENDPOINT"
	runOTELTracesEnvVar    = "BOTFAM_RUN_OTEL_TRACES_FILE"
	runOTELTraceFlushWait  = time.Second
)

var (
	forgeIssueRefRE = regexp.MustCompile(`#([0-9]+)`)
	wikiPageRefRE   = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
)

type issueClient interface {
	GetIssue(ctx context.Context, num int) (*forge.Issue, error)
	GetWikiPage(ctx context.Context, name string) (*forge.WikiPage, error)
}

type runFailureClass string

type harnessResult struct {
	Status      runFailureClass
	ExitCode    int
	Signal      string
	CommandLine string
	Stdout      string
	Stderr      string
	Transcript  []map[string]any
	Artifacts   map[string]any
	TokenUsage  map[string]any
}

type runGoal struct {
	Prompt   string        `json:"prompt"`
	Entities []forgeEntity `json:"entities,omitempty"`
}

type forgeEntity struct {
	Kind   string `json:"kind"`
	Owner  string `json:"owner,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number,omitempty"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	Error  string `json:"error,omitempty"`
}

type runEnvelope struct {
	RunID          string         `json:"run_id"`
	StartAt        string         `json:"start_at"`
	EndAt          string         `json:"end_at"`
	DurationMs     int64          `json:"duration_ms"`
	IssueNumber    int            `json:"issue_number"`
	IssueURL       string         `json:"issue_url"`
	IssueTitle     string         `json:"issue_title"`
	Goal           runGoal        `json:"goal"`
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
		Short:         "Run a goal prompt through an agent harness (prototype)",
		Long:          runCommandHelp,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: cmdutil.RunWithRegistryCtx(func(ctx context.Context, cmd *cobra.Command, args []string) error {
			ro := opts
			ro.output = cmd.OutOrStdout()
			if ro.timeoutS < 0 {
				return fmt.Errorf("--timeout must be >= 0")
			}
			if ro.issue < 0 {
				return fmt.Errorf("--issue must be >= 0")
			}
			if ro.issue == 0 && strings.TrimSpace(ro.prompt) == "" {
				return fmt.Errorf("--issue or --prompt is required")
			}
			if ro.harness != "" && ro.agent != "" && ro.harness != ro.agent {
				return fmt.Errorf("--agent and --harness disagree; pass only one")
			}
			fctx, ok := famctx.FromContext(ctx)
			if !ok {
				return fmt.Errorf("run: missing family context")
			}
			ro = defaultRunOptions(ro, fctx)
			client, err := forge.NewClient(ctx)
			if err != nil {
				return err
			}
			return runIssue(ctx, client, fctx, ro)
		}),
	}
	flags := c.Flags()
	flags.IntVar(&opts.issue, "issue", 0, "forge issue number to run (sugar for a goal prompt)")
	flags.StringVar(&opts.agent, "agent", "", "agent override (default: detected agent, then codex)")
	flags.StringVar(&opts.harness, "harness", "", "deprecated alias for --agent")
	flags.StringVar(&opts.target, "target", "", "harness target for fake mode (e.g. success, fail, sleep:2s)")
	flags.StringVar(&opts.harnessCmd, "harness-command", "", "command for --target harness (or set BOTFAM_RUN_HARNESS_CMD)")
	flags.StringVar(&opts.prompt, "prompt", "", "goal prompt sent to the agent; may mention forge entities such as #444")
	flags.BoolVar(&opts.verbose, "verbose", false, "print artifact files after the run")
	flags.StringVar(&opts.otel, "otel-endpoint", os.Getenv(runOTELEndpointEnvVar), "OTLP/HTTP traces endpoint for harness telemetry (or set BOTFAM_RUN_OTEL_ENDPOINT)")
	flags.StringVar(&opts.otelTraces, "otel-traces-file", os.Getenv(runOTELTracesEnvVar), "collector JSONL trace file to copy into the run directory (default with --otel-endpoint: <worktree>/scratch/otel/traces.jsonl)")
	flags.IntVar(&opts.timeoutS, "timeout", 0, "wall-clock timeout in seconds (0 = no timeout)")
	flags.StringVar(&opts.captureDir, "capture-dir", "", "capture directory (default: $FAMDIR/runs)")
	_ = c.Flags().MarkDeprecated("harness", "use --agent")
	return c
}

func defaultRunOptions(opts runOptions, fctx famctx.Context) runOptions {
	opts.agentSet = opts.agent != "" || opts.harness != ""
	if opts.agent == "" {
		opts.agent = opts.harness
	}
	if opts.agent == "" {
		opts.agent = fctx.Harness
	}
	if opts.agent == "" {
		opts.agent = runDefaultHarness
	}
	if opts.target == "" {
		if opts.agentSet {
			opts.target = runTargetHarnessPrefix
		} else {
			opts.target = runTargetSuccessPrefix
		}
	}
	return opts
}

func runIssue(ctx context.Context, client issueClient, fctx famctx.Context, opts runOptions) error {
	opts = defaultRunOptions(opts, fctx)
	var issue *forge.Issue
	if opts.issue > 0 {
		var err error
		issue, err = client.GetIssue(ctx, opts.issue)
		if err != nil {
			return fmt.Errorf("run: fetch issue %d: %w", opts.issue, err)
		}
	}
	captureRoot := opts.captureDir
	if captureRoot == "" {
		captureRoot = filepath.Join(fctx.FamDir, "runs")
	}
	runID := makeRunID(opts.issue, opts.agent)
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
	runEnv, runRepo := resolveIssueInfo(fctx.Registry)
	if issue != nil && runRepo == "" && issue.Repository != nil && issue.Repository.FullName != "" {
		runRepo = issue.Repository.FullName
	}
	issueURL := issueURL(issue, runEnv, runRepo, opts.issue)
	goal, err := buildRunGoal(runCtx, client, opts, issue, issueURL, runEnv, runRepo)
	if err != nil {
		return err
	}
	harnessPrompt := buildHarnessPrompt(goal, issue, issueURL)
	otelTracesFile := resolveOTELTracesFile(opts.otel, opts.otelTraces, fctx.WorktreeRoot)
	otelTraceStart := traceFileOffset(otelTracesFile)
	hResult := runHarnessTarget(runCtx, opts.agent, opts.target, opts.harnessCmd, opts.issue, harnessPrompt, fctx.WorktreeRoot, runDir, opts.otel)
	end := time.Now().UTC()
	status := hResult.Status
	if status == runStatusSuccess && runCtx.Err() != nil {
		status = runStatusTimeout
		if runCtx.Err() == context.Canceled {
			status = runStatusCancelled
		}
	}

	artifact := runEnvelope{
		RunID:        runID,
		StartAt:      start.Format(time.RFC3339Nano),
		EndAt:        end.Format(time.RFC3339Nano),
		DurationMs:   end.Sub(start).Milliseconds(),
		IssueNumber:  opts.issue,
		IssueURL:     issueURL,
		IssueTitle:   issueTitle(issue),
		Goal:         goal,
		Harness:      opts.agent,
		HarnessCmd:   hResult.CommandLine,
		Target:       opts.target,
		WorktreePath: fctx.WorktreeRoot,
		Branch:       branch,
		FailureClass: string(status),
		ExitCode:     hResult.ExitCode,
		Signal:       hResult.Signal,
		ForgeArtifacts: map[string]any{
			"comments":      []string{},
			"issues":        []string{},
			"pull_requests": []string{},
		},
		TokenUsage: hResult.TokenUsage,
	}
	if artifact.TokenUsage == nil {
		artifact.TokenUsage = map[string]any{
			"source": "unavailable",
		}
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
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte(harnessPrompt), 0o644); err != nil {
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
		"issue_number": opts.issue,
		"goal":         goal,
		"harness":      opts.agent,
		"target":       opts.target,
		"working_tree": fctx.WorktreeRoot,
		"repository":   runRepo,
		"status":       string(status),
	}
	if opts.otel != "" {
		redactedEnv["otel_endpoint"] = opts.otel
	}
	if otelTracesFile != "" {
		redactedEnv["otel_traces_file"] = otelTracesFile
	}
	if err := writeJSON(filepath.Join(runDir, "env.redacted.json"), redactedEnv); err != nil {
		return fmt.Errorf("run: write env.redacted.json: %w", err)
	}
	if otelTracesFile != "" {
		if err := copyOTELTraceDelta(filepath.Join(runDir, "otel-traces.jsonl"), otelTracesFile, otelTraceStart); err != nil {
			return fmt.Errorf("run: write otel-traces.jsonl: %w", err)
		}
	}

	if err := printRunArtifacts(opts.output, runDir, opts.verbose); err != nil {
		return err
	}

	if status != runStatusSuccess {
		return fmt.Errorf("run %s finished with status %s", runID, status)
	}
	return nil
}

func runHarnessTarget(ctx context.Context, harness, target, harnessCommand string, issue int, harnessPrompt, worktreeRoot, runDir, otelEndpoint string) harnessResult {
	cmd := fmt.Sprintf("fake-harness --harness=%s --target=%s --issue=%d", harness, target, issue)
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
					{"event": "target", "issue": issue},
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
					{"event": "target", "issue": issue},
				},
			}
		}
	case target == runTargetSuccessPrefix || target == "env":
		return runBashHarness(ctx, "env", cmd, int64(issue), worktreeRoot)
	case strings.HasPrefix(target, "shell:"):
		script := strings.TrimSpace(strings.TrimPrefix(target, "shell:"))
		if script == "" {
			script = "env"
		}
		return runBashHarness(ctx, script, cmd, int64(issue), worktreeRoot)
	case target == runTargetOllamaPrefix || strings.HasPrefix(target, runTargetOllamaPrefix+":"):
		return runBashHarness(ctx, runBashOllamaCommand(target), cmd, int64(issue), worktreeRoot)
	case target == runTargetHarnessPrefix || strings.HasPrefix(target, runTargetHarnessPrefix+":"):
		command := ""
		if strings.HasPrefix(target, runTargetHarnessPrefix+":") {
			command = strings.TrimSpace(strings.TrimPrefix(target, runTargetHarnessPrefix+":"))
		}
		resolved, ok := resolveHarnessCommand(command, harnessCommand)
		if !ok {
			return runHarnessCLI(ctx, harness, harnessPrompt, cmd, int64(issue), worktreeRoot, runDir, otelEndpoint)
		}
		return runBashHarness(ctx, resolved, resolved, int64(issue), worktreeRoot)
	default:
		return runBashHarness(ctx, "env", cmd, int64(issue), worktreeRoot)
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

func runHarnessCLI(ctx context.Context, harness, prompt, fallbackCommand string, issue int64, worktreeRoot, runDir, otelEndpoint string) harnessResult {
	switch famconfig.CanonicalHarness(harness) {
	case famconfig.HarnessCodex:
		return runCodexHarnessCommand(ctx, prompt, issue, worktreeRoot, runDir, otelEndpoint)
	case famconfig.HarnessClaudeCode:
		return runClaudeHarnessCommand(ctx, prompt, issue, worktreeRoot)
	case famconfig.HarnessAntigravity:
		return runDirectHarnessCommand(ctx, "agy", []string{"--print", prompt}, issue, worktreeRoot)
	}
	return harnessResult{
		Status:      runStatusRunnerError,
		ExitCode:    4,
		CommandLine: fallbackCommand,
		Stderr:      "run: no harness binary found for harness " + harness + "; use --harness-command or --target harness:<command>\n",
	}
}

func runClaudeHarnessCommand(ctx context.Context, prompt string, issue int64, worktreeRoot string) harnessResult {
	args := []string{
		"-p",
		prompt,
		"--verbose",
		"--output-format",
		"stream-json",
		"--include-hook-events",
		"--include-partial-messages",
	}
	result := runDirectHarnessCommand(ctx, "claude", args, issue, worktreeRoot)
	if parsed := parseStreamJSONTranscript(result.Stdout); len(parsed) > 0 {
		result.Transcript = parsed
		result.TokenUsage = tokenUsageFromClaudeTranscript(parsed)
	}
	return result
}

func runCodexHarnessCommand(ctx context.Context, prompt string, issue int64, worktreeRoot, runDir, otelEndpoint string) harnessResult {
	args := []string{"exec", "--json"}
	if worktreeRoot != "" {
		args = append(args, "-C", worktreeRoot)
	}
	if runDir != "" {
		args = append(args, "-o", filepath.Join(runDir, "final.md"))
	}
	if otelEndpoint = strings.TrimSpace(otelEndpoint); otelEndpoint != "" {
		args = append(args,
			"-c", codexOtelTraceExporterOverride(otelEndpoint),
			"-c", `otel.metrics_exporter="none"`,
		)
	}
	args = append(args, prompt)
	result := runDirectHarnessCommand(ctx, "codex", args, issue, worktreeRoot)
	if parsed := parseStreamJSONTranscript(result.Stdout); len(parsed) > 0 {
		result.Transcript = parsed
		result.TokenUsage = tokenUsageFromCodexTranscript(parsed)
	}
	return result
}

func codexOtelTraceExporterOverride(endpoint string) string {
	return fmt.Sprintf(`otel.trace_exporter={ otlp-http = { endpoint = %q, protocol = "json" } }`, endpoint)
}

func resolveOTELTracesFile(otelEndpoint, configuredPath, worktreeRoot string) string {
	if configuredPath = strings.TrimSpace(configuredPath); configuredPath != "" {
		return configuredPath
	}
	if strings.TrimSpace(otelEndpoint) == "" || worktreeRoot == "" {
		return ""
	}
	return filepath.Join(worktreeRoot, "scratch", "otel", "traces.jsonl")
}

func traceFileOffset(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func copyOTELTraceDelta(destPath, sourcePath string, offset int64) error {
	data, err := readOTELTraceDelta(sourcePath, offset, runOTELTraceFlushWait)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0o644)
}

func readOTELTraceDelta(sourcePath string, offset int64, wait time.Duration) ([]byte, error) {
	if sourcePath == "" {
		return nil, nil
	}
	deadline := time.Now().Add(wait)
	var lastInfo os.FileInfo
	var lastErr error
	for {
		info, err := os.Stat(sourcePath)
		if err == nil && !info.IsDir() {
			lastInfo = info
			if info.Size() > offset || time.Now().After(deadline) {
				break
			}
		} else if err != nil && !os.IsNotExist(err) {
			lastErr = err
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if lastInfo == nil {
		return []byte{}, nil
	}
	if lastInfo.Size() < offset {
		offset = 0
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []byte{}, nil
		}
		return nil, err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

func tokenUsageFromCodexTranscript(rows []map[string]any) map[string]any {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i]["type"] != "turn.completed" {
			continue
		}
		usage, ok := rows[i]["usage"].(map[string]any)
		if !ok {
			continue
		}
		out := map[string]any{"source": "stream_event"}
		for _, key := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens"} {
			if n, ok := numericJSONValue(usage[key]); ok {
				out[key] = n
			}
		}
		if total, ok := sumTokensSpent(out); ok {
			out["total_tokens"] = total
		}
		return out
	}
	return nil
}

// tokenUsageFromClaudeTranscript extracts token usage from Claude Code's
// stream-json output. Claude emits a final {"type":"result", ..., "usage":{...},
// "total_cost_usd":N} event whose usage object mirrors the Anthropic API shape
// (input_tokens, output_tokens, cache_read_input_tokens,
// cache_creation_input_tokens). Those are mapped onto the same envelope keys the
// Codex extractor uses (cached_input_tokens <- cache_read_input_tokens) so the
// two harnesses stay comparable, and it additionally records cache-creation
// tokens and the reported dollar cost — neither of which Codex emits.
func tokenUsageFromClaudeTranscript(rows []map[string]any) map[string]any {
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i]["type"] != "result" {
			continue
		}
		usage, ok := rows[i]["usage"].(map[string]any)
		if !ok {
			continue
		}
		out := map[string]any{"source": "stream_event"}
		for _, m := range []struct{ src, dst string }{
			{"input_tokens", "input_tokens"},
			{"output_tokens", "output_tokens"},
			{"cache_read_input_tokens", "cached_input_tokens"},
			{"cache_creation_input_tokens", "cache_creation_input_tokens"},
		} {
			if n, ok := numericJSONValue(usage[m.src]); ok {
				out[m.dst] = n
			}
		}
		if total, ok := sumTokensSpent(out); ok {
			out["total_tokens"] = total
		}
		// Claude reports a running dollar cost directly; Codex does not.
		if cost, ok := rows[i]["total_cost_usd"].(float64); ok {
			out["total_cost_usd"] = cost
		}
		return out
	}
	return nil
}

// sumTokensSpent totals the tokens actually spent against the model — input +
// output + every cache counter — per the session-metric convention in
// skills/botfam-session-retrospective/SKILL.md ("total input + output + cache
// tokens spent"). It deliberately excludes reasoning_output_tokens, which
// harnesses report as a subset of output_tokens (summing it would double-count).
// Applied identically to both harness extractors so total_tokens is comparable.
func sumTokensSpent(out map[string]any) (int64, bool) {
	var total int64
	present := false
	for _, key := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "cache_creation_input_tokens"} {
		if n, ok := out[key].(int64); ok {
			total += n
			present = true
		}
	}
	return total, present
}

func numericJSONValue(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func parseStreamJSONTranscript(raw string) []map[string]any {
	rows := make([]map[string]any, 0)
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			rows = append(rows, map[string]any{
				"event":      "raw_line",
				"line":       lineNo,
				"raw_output": line,
				"error":      err.Error(),
			})
			continue
		}
		if row != nil {
			rows = append(rows, row)
		}
	}
	if err := scanner.Err(); err != nil {
		rows = append(rows, map[string]any{
			"event": "stream_scan_error",
			"error": err.Error(),
		})
	}
	return rows
}

func runDirectHarnessCommand(ctx context.Context, command string, args []string, issue int64, worktreeRoot string) harnessResult {
	cmd := exec.CommandContext(ctx, command, args...)
	if worktreeRoot != "" {
		cmd.Dir = worktreeRoot
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	status := runFailureClass(runStatusSuccess)
	exitCode := 0
	signal := ""
	if err != nil {
		status, exitCode, signal = harnessExitDetails(ctx, err)
	}

	return harnessResult{
		Status:      status,
		ExitCode:    exitCode,
		Signal:      signal,
		CommandLine: commandLineForArgs(command, args),
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		Transcript: []map[string]any{
			{"event": "harness", "command": command, "args": args},
			{"event": "issue", "index": issue},
		},
		Artifacts: map[string]any{
			"command": command,
			"args":    args,
		},
	}
}

func commandLineForArgs(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, command)
	for _, arg := range args {
		parts = append(parts, strconv.Quote(arg))
	}
	return strings.Join(parts, " ")
}

func harnessExitDetails(ctx context.Context, err error) (runFailureClass, int, string) {
	if ctx.Err() == context.DeadlineExceeded {
		return runFailureClass(runStatusTimeout), 124, ""
	}
	if ctx.Err() == context.Canceled {
		return runFailureClass(runStatusCancelled), 124, ""
	}

	exitCode := 1
	signal := ""
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal = status.Signal().String()
			// ExitCode reports -1 when the process did not exit normally.
			// Persist the signal explicitly and use the conventional shell code.
			exitCode = 128 + int(status.Signal())
		}
	}
	if exitCode < 0 {
		exitCode = 1
	}
	return runFailureClass(runStatusToolError), exitCode, signal
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

func runBashHarness(ctx context.Context, shellCommand, commandLine string, issue int64, worktreeRoot string) harnessResult {
	bash := exec.CommandContext(ctx, "bash", "-lc", shellCommand)
	if worktreeRoot != "" {
		bash.Dir = worktreeRoot
	}
	var stdout, stderr bytes.Buffer
	bash.Stdout = &stdout
	bash.Stderr = &stderr
	err := bash.Run()

	status := runFailureClass(runStatusSuccess)
	exitCode := 0
	signal := ""
	if err != nil {
		status, exitCode, signal = harnessExitDetails(ctx, err)
	}

	return harnessResult{
		Status:      status,
		ExitCode:    exitCode,
		Signal:      signal,
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
	if num <= 0 {
		return ""
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

func buildRunGoal(ctx context.Context, client issueClient, opts runOptions, issue *forge.Issue, primaryIssueURL, baseURL, repo string) (runGoal, error) {
	goal := fallbackRunGoal(opts, issue, primaryIssueURL, repo)

	for _, n := range extractIssueRefs(goal.Prompt) {
		if issue != nil && int(issue.Index) == n {
			continue
		}
		resolved, err := client.GetIssue(ctx, n)
		if err != nil {
			goal.Entities = upsertIssueEntity(goal.Entities, forgeEntity{
				Kind:   "issue",
				Repo:   repo,
				Number: n,
				URL:    issueURLForNumber(baseURL, repo, n),
				Error:  err.Error(),
			})
			continue
		}
		if resolved == nil {
			goal.Entities = upsertIssueEntity(goal.Entities, forgeEntity{
				Kind:   "issue",
				Repo:   repo,
				Number: n,
				URL:    issueURLForNumber(baseURL, repo, n),
				Error:  "issue lookup returned no issue",
			})
			continue
		}
		entityRepo := repo
		if resolved.Repository != nil && resolved.Repository.FullName != "" {
			entityRepo = resolved.Repository.FullName
		}
		goal.Entities = upsertIssueEntity(goal.Entities, forgeEntity{
			Kind:   issueEntityKind(resolved),
			Repo:   entityRepo,
			Number: int(resolved.Index),
			Name:   resolved.Title,
			URL:    issueURL(resolved, baseURL, entityRepo, int(resolved.Index)),
		})
	}

	for _, name := range extractWikiRefs(goal.Prompt) {
		page, err := client.GetWikiPage(ctx, name)
		if err != nil {
			goal.Entities = upsertWikiEntity(goal.Entities, forgeEntity{
				Kind:  "wiki_page",
				Repo:  repo,
				Name:  name,
				URL:   wikiURL(baseURL, repo, name),
				Error: err.Error(),
			})
			continue
		}
		if page == nil {
			goal.Entities = upsertWikiEntity(goal.Entities, forgeEntity{
				Kind:  "wiki_page",
				Repo:  repo,
				Name:  name,
				URL:   wikiURL(baseURL, repo, name),
				Error: "wiki lookup returned no page",
			})
			continue
		}
		pageName := name
		if page.Title != "" {
			pageName = page.Title
		}
		pageURL := page.HTMLURL
		if pageURL == "" {
			pageURL = wikiURL(baseURL, repo, name)
		}
		goal.Entities = upsertWikiEntity(goal.Entities, forgeEntity{
			Kind: "wiki_page",
			Repo: repo,
			Name: pageName,
			URL:  pageURL,
		})
	}

	return goal, nil
}

func fallbackRunGoal(opts runOptions, issue *forge.Issue, issueURL, repo string) runGoal {
	prompt := strings.TrimSpace(opts.prompt)
	if prompt == "" && issue != nil {
		prompt = fmt.Sprintf("Implement issue #%d: %s", issue.Index, issue.Title)
	}

	goal := runGoal{
		Prompt:   prompt,
		Entities: unresolvedForgeEntities(prompt, repo),
	}
	if issue != nil {
		goal.Entities = upsertIssueEntity(goal.Entities, forgeEntity{
			Kind:   issueEntityKind(issue),
			Repo:   repo,
			Number: int(issue.Index),
			Name:   issue.Title,
			URL:    issueURL,
		})
	}
	return goal
}

func unresolvedForgeEntities(prompt, repo string) []forgeEntity {
	seen := map[int]bool{}
	var entities []forgeEntity
	for _, n := range extractIssueRefs(prompt) {
		if seen[n] {
			continue
		}
		seen[n] = true
		entities = append(entities, forgeEntity{
			Kind:   "issue",
			Repo:   repo,
			Number: n,
		})
	}
	seenWiki := map[string]bool{}
	for _, name := range extractWikiRefs(prompt) {
		if seenWiki[name] {
			continue
		}
		seenWiki[name] = true
		entities = append(entities, forgeEntity{
			Kind: "wiki_page",
			Repo: repo,
			Name: name,
		})
	}
	return entities
}

func extractIssueRefs(prompt string) []int {
	matches := forgeIssueRefRE.FindAllStringSubmatch(prompt, -1)
	refs := make([]int, 0, len(matches))
	seen := map[int]bool{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		n, err := strconv.Atoi(match[1])
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		refs = append(refs, n)
	}
	return refs
}

func extractWikiRefs(prompt string) []string {
	matches := wikiPageRefRE.FindAllStringSubmatch(prompt, -1)
	refs := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		refs = append(refs, name)
	}
	return refs
}

func upsertIssueEntity(entities []forgeEntity, issue forgeEntity) []forgeEntity {
	for i, entity := range entities {
		if (entity.Kind == "issue" || entity.Kind == "pr") && entity.Number == issue.Number {
			entities[i] = mergeForgeEntity(entity, issue)
			return entities
		}
	}
	return append([]forgeEntity{issue}, entities...)
}

func upsertWikiEntity(entities []forgeEntity, wiki forgeEntity) []forgeEntity {
	for i, entity := range entities {
		if entity.Kind == "wiki_page" && entity.Name == wiki.Name {
			entities[i] = mergeForgeEntity(entity, wiki)
			return entities
		}
	}
	return append(entities, wiki)
}

func mergeForgeEntity(existing, resolved forgeEntity) forgeEntity {
	if resolved.Kind != "" {
		existing.Kind = resolved.Kind
	}
	if existing.Owner == "" {
		existing.Owner = resolved.Owner
	}
	if existing.Repo == "" {
		existing.Repo = resolved.Repo
	}
	if existing.Name == "" {
		existing.Name = resolved.Name
	}
	if existing.URL == "" {
		existing.URL = resolved.URL
	}
	if existing.Error == "" {
		existing.Error = resolved.Error
	}
	return existing
}

func issueURLForNumber(baseURL, repo string, n int) string {
	if n <= 0 || baseURL == "" || repo == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/issues/%d", strings.TrimSuffix(baseURL, "/"), repo, n)
}

func wikiURL(baseURL, repo, name string) string {
	if baseURL == "" || repo == "" || name == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/wiki/%s", strings.TrimSuffix(baseURL, "/"), repo, url.PathEscape(name))
}

func issueEntityKind(issue *forge.Issue) string {
	if issue != nil && issue.PullRequest != nil {
		return "pr"
	}
	return "issue"
}

func issueTitle(issue *forge.Issue) string {
	if issue == nil {
		return ""
	}
	return issue.Title
}

func buildHarnessPrompt(goal runGoal, issue *forge.Issue, issueURL string) string {
	var b strings.Builder
	b.WriteString("# Goal\n\n")
	b.WriteString(goal.Prompt)
	b.WriteString("\n\n")
	if len(goal.Entities) > 0 {
		b.WriteString("## Referenced Forge Entities\n\n")
		for _, entity := range goal.Entities {
			b.WriteString("- ")
			b.WriteString(entity.Kind)
			if entity.Number > 0 {
				b.WriteString(" #")
				b.WriteString(strconv.Itoa(entity.Number))
			}
			if entity.Repo != "" {
				b.WriteString(" (")
				b.WriteString(entity.Repo)
				b.WriteString(")")
			}
			if entity.Name != "" {
				b.WriteString(": ")
				b.WriteString(entity.Name)
			}
			if entity.URL != "" {
				b.WriteString(" ")
				b.WriteString(entity.URL)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if issue == nil {
		return b.String()
	}
	b.WriteString("## Resolved Issue Context\n\n")
	b.WriteString("URL: ")
	b.WriteString(issueURL)
	b.WriteString("\n\nTitle: ")
	b.WriteString(issue.Title)
	b.WriteString("\n\n")
	b.WriteString("Body:\n\n")
	b.WriteString(issue.Body)
	b.WriteString("\n")
	return b.String()
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

func printRunArtifacts(w io.Writer, runDir string, verbose bool) error {
	if w == nil {
		return nil
	}
	fmt.Fprintf(w, "run artifacts: %s\n", runDir)
	if !verbose {
		return nil
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return fmt.Errorf("run: list artifact directory %s: %w", runDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fmt.Fprintf(w, "  %s\n", filepath.Join(runDir, entry.Name()))
	}
	return nil
}
