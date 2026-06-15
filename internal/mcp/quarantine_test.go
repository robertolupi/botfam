package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const quarantineFamTOML = `name = "myfam"
slug = "myfam"
roster = ["alice"]

[agent.alice]
harness = "claude-code"
`

// quarantineFam writes a fam.toml declaring [agent.alice] into a fresh fam dir
// and carves the alice agent worktree (and thus baseDir/main, the base checkout)
// out of it. It returns (agentWorktreeDir, baseCheckoutDir).
func quarantineFam(t *testing.T) (agentDir, mainDir string) {
	t.Helper()
	baseDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = eval
	}
	if err := os.WriteFile(filepath.Join(baseDir, "fam.toml"), []byte(quarantineFamTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	agentDir = setupTestWorktree(t, baseDir, "alice", "alice")
	return agentDir, filepath.Join(baseDir, "main")
}

// TestServeGateValidAgentWorktreeServesNormalTools — case (a): in a valid
// [agent.<name>] worktree the gate is inert and a normal tool dispatches.
func TestServeGateValidAgentWorktreeServesNormalTools(t *testing.T) {
	s, _ := newTestServer(t)

	wtDir, _ := quarantineFam(t)

	// irc_read needs a log file to get past the gate and reach its handler.
	logDir := filepath.Join(wtDir, "scratch", "irc", "alice")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "log"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": wtDir}); err != nil {
		t.Fatalf("valid agent worktree should serve irc_read, got: %v", err)
	}
}

// TestServeGateNonAgentWorktreeQuarantined — case (b): in the base/main checkout
// (fam.toml present, but not a declared [agent.<name>]) a normal tool is refused
// with the quarantine error pointing at botfam:///problem, and the problem
// resource carries the diagnosis.
func TestServeGateNonAgentWorktreeQuarantined(t *testing.T) {
	s, _ := newTestServer(t)

	_, mainDir := quarantineFam(t)

	_, err := s.callTool(context.Background(), "irc_read", map[string]any{"work_dir": mainDir})
	if err == nil {
		t.Fatal("expected quarantine error from the base/main checkout, got nil")
	}
	if !strings.Contains(err.Error(), "quarantined") {
		t.Errorf("expected a quarantine error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "botfam:///problem") {
		t.Errorf("quarantine error should point at botfam:///problem, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "operator") {
		t.Errorf("quarantine error should tell the agent to report to its operator, got %q", err.Error())
	}

	// Read botfam:///problem with CWD inside the quarantined checkout (matches how
	// the param-less resource resolves its work dir, like TestMcpResources).
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(mainDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	req := mcplib.ReadResourceRequest{}
	req.Params.URI = "botfam:///problem"
	res, perr := s.handleReadResource(context.Background(), req)
	if perr != nil {
		t.Fatalf("reading botfam:///problem failed: %v", perr)
	}
	txt := res[0].(mcplib.TextResourceContents).Text
	if !strings.Contains(txt, "MISCONFIGURED") || !strings.Contains(txt, "report") {
		t.Errorf("problem resource missing quarantine banner/report guidance, got %q", txt)
	}

	req.Params.URI = "botfam:///problem.json"
	res, perr = s.handleReadResource(context.Background(), req)
	if perr != nil {
		t.Fatalf("reading botfam:///problem.json failed: %v", perr)
	}
	var p struct {
		Schema           string `json:"schema"`
		Status           string `json:"status"`
		Failure          string `json:"failure"`
		ReportToOperator bool   `json:"report_to_operator"`
	}
	if err := json.Unmarshal([]byte(res[0].(mcplib.TextResourceContents).Text), &p); err != nil {
		t.Fatalf("problem.json is not valid JSON: %v", err)
	}
	if p.Schema != "botfam.problem.v1" {
		t.Errorf("problem.json schema = %q, want botfam.problem.v1", p.Schema)
	}
	if p.Status != "quarantined" {
		t.Errorf("problem.json status = %q, want quarantined", p.Status)
	}
	if !p.ReportToOperator || p.Failure == "" {
		t.Errorf("problem.json should flag report_to_operator with a failure reason, got %+v", p)
	}
}

// TestServeGateOrientWorksInQuarantine — case (c): orient, the read-only
// diagnosis probe, remains callable in a quarantined worktree.
func TestServeGateOrientWorksInQuarantine(t *testing.T) {
	s, _ := newTestServer(t)

	_, mainDir := quarantineFam(t)

	res, err := s.callTool(context.Background(), "orient", map[string]any{"work_dir": mainDir})
	if err != nil {
		t.Fatalf("orient must work in quarantine, got: %v", err)
	}
	var out struct {
		Schema string `json:"schema"`
	}
	decodeToolResult(t, res, &out)
	if out.Schema != "botfam.discovery.v1" {
		t.Errorf("orient should return discovery JSON even in quarantine, got schema %q", out.Schema)
	}
}
