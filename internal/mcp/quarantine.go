package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
)

// famConfigured reports whether workDir belongs to a registered fam: a
// `[repo.<k>]` stanza in ~/.botfam/config.toml whose path is an ancestor of
// workDir resolves (#404). This distinguishes a registered fam — where a
// ResolveFam failure means "wrong/base/user worktree" and must quarantine — from
// an unregistered dir, where the quarantine gate must not engage.
func famConfigured(workDir string) bool {
	_, err := famconfig.ResolveConfig(workDir)
	return err == nil
}

// Fail-closed serve gate (#191, proposal-unified-fam-config §4.6).
//
// When `botfam serve` runs in a worktree that is NOT a valid agent worktree —
// famconfig.ResolveFam(workDir) returns an error (not inside a git worktree, no
// matching [repo.<k>] stanza in ~/.botfam/config.toml, a [user.<name>] human
// checkout, or the base/main checkout) — the
// server must refuse to do real work. It still starts (so the harness gets a
// surface), but the only affordance is a quarantine resource set that tells the
// agent to report to its operator instead of self-fixing.
//
// The decision is taken per tool dispatch in callTool: every tool except
// `orient` (the read-only diagnosis probe) is refused with a clear "quarantined"
// error pointing at botfam:///problem. The botfam:///problem resource (and its
// .json sibling) carry the ResolveFam diagnosis verbatim — that text already
// ends with a "report this to your operator" hint — plus a pre-filled report
// template.

// quarantineError is returned to a normal tool call when the worktree is not a
// valid agent worktree. It mirrors the existing fmt.Errorf error style and names
// the quarantine resource so the agent can read the full diagnosis.
func quarantineError(cause error) error {
	return fmt.Errorf("quarantined: this is not a valid agent worktree, so the botfam runtime refuses to act here — read botfam:///problem and report to your operator; do not self-fix (%v)", cause)
}

// renderProblemMarkdown is the human-readable botfam:///problem resource served
// in quarantine. cause is the famconfig.ResolveFam error (which already carries the
// "report to your operator" hint). When cause is nil the worktree is healthy and
// the resource says so.
func renderProblemMarkdown(workDir string, cause error) []byte {
	var b strings.Builder
	if cause == nil {
		b.WriteString("# botfam: no problem\n\n")
		b.WriteString("This worktree resolves as a valid agent worktree; the runtime is operating normally.\n")
		b.WriteString("If you reached this resource looking for a fault, there is none to report.\n")
		return []byte(b.String())
	}

	b.WriteString("# botfam: MISCONFIGURED — report to your operator\n\n")
	b.WriteString("The botfam runtime refused to start its normal tool set in this worktree because\n")
	b.WriteString("it is not a valid agent worktree. **Do not try to fix this yourself** — report\n")
	b.WriteString("it to your operator and wait. Self-debugging here wastes turns and cannot\n")
	b.WriteString("succeed: the only correct action is escalation.\n\n")

	b.WriteString("## Diagnosis\n\n")
	fmt.Fprintf(&b, "- **work_dir**: %s\n", orPlaceholder(workDir, "<unknown>"))
	fmt.Fprintf(&b, "- **failure**: %s\n", cause.Error())

	b.WriteString("\n## What this means\n\n")
	b.WriteString("`famconfig.ResolveFam` (the single source of truth for fam identity) could not accept\n")
	b.WriteString("this worktree as a declared `[agent.<name>]`. Causes include: not inside a git\n")
	b.WriteString("worktree, no matching `[repo.<k>]` stanza in `~/.botfam/config.toml`, a\n")
	b.WriteString("`[user.<name>]` (human) checkout, or the base/`main` checkout. None of these are\n")
	b.WriteString("agent runtime contexts.\n")

	b.WriteString("\n## Report this (template)\n\n")
	b.WriteString("```\n")
	b.WriteString("botfam runtime quarantined — cannot operate.\n")
	fmt.Fprintf(&b, "work_dir: %s\n", orPlaceholder(workDir, "<unknown>"))
	fmt.Fprintf(&b, "failure : %s\n", cause.Error())
	b.WriteString("I have NOT attempted to self-fix. Please advise.\n")
	b.WriteString("```\n")

	b.WriteString("\nThe normal botfam tools and resources are intentionally absent until this is\n")
	b.WriteString("resolved. The only available probe is the `orient` tool (read-only).\n")
	return []byte(b.String())
}

// problemJSON is the structured botfam:///problem.json schema.
type problemJSON struct {
	Schema      string `json:"schema"`
	Status      string `json:"status"` // "ok" | "quarantined"
	WorkDir     string `json:"work_dir"`
	Failure     string `json:"failure,omitempty"`
	ReportToOp  bool   `json:"report_to_operator"`
	SelfFixHint string `json:"self_fix"`
}

func renderProblemJSON(workDir string, cause error) ([]byte, error) {
	p := problemJSON{
		Schema:      "botfam.problem.v1",
		WorkDir:     workDir,
		ReportToOp:  cause != nil,
		SelfFixHint: "do not self-fix; report to your operator",
	}
	if cause == nil {
		p.Status = "ok"
		p.SelfFixHint = ""
		p.ReportToOp = false
	} else {
		p.Status = "quarantined"
		p.Failure = cause.Error()
	}
	return json.MarshalIndent(p, "", "  ")
}

// problemResource builds the MCP resource contents for botfam:///problem and
// botfam:///problem.json from the resolve result for workDir.
func problemResource(ctx context.Context, uri, workDir string, wantJSON bool) ([]mcplib.ResourceContents, error) {
	_, cause := famctx.Resolve(ctx, famctx.Inputs{WorkDir: workDir, Mode: famctx.ModeAgentRuntime})
	if wantJSON {
		body, err := renderProblemJSON(workDir, cause)
		if err != nil {
			return nil, err
		}
		return []mcplib.ResourceContents{mcplib.TextResourceContents{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(body),
		}}, nil
	}
	return markdownResource(uri, renderProblemMarkdown(workDir, cause)), nil
}
