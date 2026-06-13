package ccrep

import (
	"context"
	"fmt"
	"strings"
)

// maxBangLine is the scribe parser's single-PRIVMSG limit. Bang commands must
// fit one line; the parser does not reassemble splits.
const maxBangLine = 400

// Engine is the core ccrep API. It is wired with concrete ports at each
// adapter's composition root and is safe to share. All methods are
// presentation-free: they return data and never print or exit.
type Engine struct {
	vc            VersionControl
	ledger        Ledger
	tx            Transport
	actor         string
	defaultQuorum Quorum
}

// New constructs an Engine. actor is the local nick, used as the default
// executor on propose. defaultQuorum is the fam's configured default (supplied
// by the adapter from fam.toml); an empty value falls back to QuorumMajority.
func New(vc VersionControl, ledger Ledger, tx Transport, actor string, defaultQuorum Quorum) *Engine {
	return &Engine{vc: vc, ledger: ledger, tx: tx, actor: actor, defaultQuorum: defaultQuorum}
}

// Propose opens a proposal. The SHA is resolved from a.Ref (default HEAD), so
// the caller never types it.
func (e *Engine) Propose(ctx context.Context, a ProposeArgs) (ActionResult, error) {
	if a.ID == "" || a.Summary == "" {
		return ActionResult{}, fmt.Errorf("propose: id and summary are required")
	}
	q := a.Quorum
	if q == "" {
		q = e.defaultQuorum // fam default (from fam.toml, set at construction)
	}
	if q == "" {
		q = QuorumMajority
	}
	if !q.valid() {
		return ActionResult{}, fmt.Errorf("propose: invalid quorum %q (all|majority|any)", q)
	}
	sha, err := e.resolveRef(ctx, a.Ref)
	if err != nil {
		return ActionResult{}, fmt.Errorf("propose: %w", err)
	}
	executor := a.Executor
	if executor == "" {
		executor = e.actor
	}
	warnings := e.pushWarning(ctx, sha)
	line := buildProposeLine(a.ID, sha, q, executor, a.Deadline, a.Summary)
	if err := checkLineLen(line); err != nil {
		return ActionResult{}, fmt.Errorf("propose: %w", err)
	}
	if err := e.tx.Send(ctx, line); err != nil {
		return ActionResult{}, fmt.Errorf("propose: send: %w", err)
	}
	return ActionResult{ProposalID: a.ID, SHA: sha, Line: line, Warnings: warnings}, nil
}

// Revise points an existing proposal at a new commit (resolved from a.Ref,
// default HEAD).
func (e *Engine) Revise(ctx context.Context, a ReviseArgs) (ActionResult, error) {
	if a.ID == "" {
		return ActionResult{}, fmt.Errorf("revise: id is required")
	}
	sha, err := e.resolveRef(ctx, a.Ref)
	if err != nil {
		return ActionResult{}, fmt.Errorf("revise: %w", err)
	}
	warnings := e.pushWarning(ctx, sha)
	line := buildRevisionLine(a.ID, sha)
	if err := e.tx.Send(ctx, line); err != nil {
		return ActionResult{}, fmt.Errorf("revise: send: %w", err)
	}
	return ActionResult{ProposalID: a.ID, SHA: sha, Line: line, Warnings: warnings}, nil
}

// Vote casts a vote. The SHA is resolved from the proposal's current revision
// in the ledger — never from local HEAD — so a reviewer always binds to what
// was proposed. The optional Expect guard fails if the proposal has moved.
func (e *Engine) Vote(ctx context.Context, a VoteArgs) (ActionResult, error) {
	if a.ID == "" {
		return ActionResult{}, fmt.Errorf("vote: id is required")
	}
	if !a.Verdict.valid() {
		return ActionResult{}, fmt.Errorf("vote: invalid verdict %q (approve|request_changes|comment)", a.Verdict)
	}
	t, err := e.ledger.Tally(ctx, a.ID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("vote: tally %q: %w", a.ID, err)
	}
	if t.LatestSHA == "" {
		return ActionResult{}, fmt.Errorf("vote: proposal %q has no current commit", a.ID)
	}
	if a.Expect != "" && !shaMatches(t.LatestSHA, a.Expect) {
		return ActionResult{}, fmt.Errorf(
			"vote: --expect %s but proposal %q is now at %s (it moved since you reviewed)",
			a.Expect, a.ID, short(t.LatestSHA))
	}
	line := buildVoteLine(a.ID, t.LatestSHA, a.Verdict)
	if err := checkLineLen(line); err != nil {
		return ActionResult{}, fmt.Errorf("vote: %w", err)
	}
	if err := e.tx.Send(ctx, line); err != nil {
		return ActionResult{}, fmt.Errorf("vote: send: %w", err)
	}
	// The review body rides alongside the verdict as ordinary channel lines
	// (the client auto-splits any >400 B). One call, no manual chunking.
	for _, bl := range bodyLines(a.Body) {
		if err := e.tx.Send(ctx, bl); err != nil {
			return ActionResult{}, fmt.Errorf("vote: send body: %w", err)
		}
	}
	return ActionResult{ProposalID: a.ID, SHA: t.LatestSHA, Line: line}, nil
}

// Tally returns the current projected state of a proposal (local read).
func (e *Engine) Tally(ctx context.Context, a TallyArgs) (TallyResult, error) {
	if a.ID == "" {
		return TallyResult{}, fmt.Errorf("tally: id is required")
	}
	return e.ledger.Tally(ctx, a.ID)
}

// Merge is the executor step: it verifies consensus from the ledger, performs
// `git merge --no-ff` of the approved commit in the main checkout, and
// announces !executed. It deliberately stops before `git push` — the push is a
// manual Operator step.
func (e *Engine) Merge(ctx context.Context, a MergeArgs) (MergeResult, error) {
	if a.ID == "" {
		return MergeResult{}, fmt.Errorf("merge: id is required")
	}
	t, err := e.ledger.Tally(ctx, a.ID)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: tally %q: %w", a.ID, err)
	}
	if t.Status != StatusApproved {
		return MergeResult{}, fmt.Errorf("merge: proposal %q is %q, not approved", a.ID, t.Status)
	}
	if t.LatestSHA == "" {
		return MergeResult{}, fmt.Errorf("merge: proposal %q has no current commit", a.ID)
	}
	dir, err := e.vc.MainCheckout(ctx)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: locate main checkout: %w", err)
	}
	msg := fmt.Sprintf("Merge proposal %s (%s)", a.ID, short(t.LatestSHA))
	head, err := e.vc.MergeNoFF(ctx, dir, t.LatestSHA, msg)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge: %w", err)
	}
	line := buildExecutedLine(a.ID, head)
	if err := e.tx.Send(ctx, line); err != nil {
		return MergeResult{ProposalID: a.ID, MergedSHA: t.LatestSHA, HeadSHA: head},
			fmt.Errorf("merge: merged %s but failed to announce: %w", short(head), err)
	}
	return MergeResult{ProposalID: a.ID, MergedSHA: t.LatestSHA, HeadSHA: head, Line: line}, nil
}

// Gate reports whether a proposal is clear to merge, without side effects.
func (e *Engine) Gate(ctx context.Context, a GateArgs) (GateResult, error) {
	if a.ID == "" {
		return GateResult{}, fmt.Errorf("gate: id is required")
	}
	t, err := e.ledger.Tally(ctx, a.ID)
	if err != nil {
		return GateResult{}, fmt.Errorf("gate: tally %q: %w", a.ID, err)
	}
	res := GateResult{ProposalID: a.ID, SHA: t.LatestSHA, Approvals: len(t.Approvals)}
	switch {
	case t.Status != StatusApproved:
		res.Reason = fmt.Sprintf("status is %q", t.Status)
	case a.Commit != "" && !shaMatches(t.LatestSHA, a.Commit):
		res.Reason = fmt.Sprintf("commit %s is not the current revision %s", short(a.Commit), short(t.LatestSHA))
	default:
		res.Passed = true
		res.Reason = fmt.Sprintf("%d independent approval(s)", len(t.Approvals))
	}
	return res, nil
}

// ListProposals delegates to the read model (for TUI/web listings).
func (e *Engine) ListProposals(ctx context.Context, f ProposalFilter) ([]ProposalView, error) {
	return e.ledger.ListProposals(ctx, f)
}

// Subscribe delegates to the read model's event stream (for reactive UIs).
func (e *Engine) Subscribe(ctx context.Context) (<-chan Event, error) {
	return e.ledger.Subscribe(ctx)
}

// --- internals ---------------------------------------------------------------

func (e *Engine) resolveRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := e.vc.RevParse(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", ref, err)
	}
	return sha, nil
}

// pushWarning returns a non-fatal advisory if the commit is not on origin.
// origin is not load-bearing for now, so this never blocks.
func (e *Engine) pushWarning(ctx context.Context, sha string) []string {
	if ok, err := e.vc.IsPushed(ctx, sha); err == nil && !ok {
		return []string{fmt.Sprintf(
			"commit %s is not reachable on origin; proceeding anyway (origin is not load-bearing for now)",
			short(sha))}
	}
	return nil
}

func buildProposeLine(id, sha string, q Quorum, executor, deadline, summary string) string {
	line := fmt.Sprintf("!propose id=%s sha=%s quorum=%s executor=%s", id, sha, q, executor)
	if deadline != "" {
		line += " deadline=" + deadline
	}
	// summary is last and quoted so the parser folds the rest of the line into it.
	line += fmt.Sprintf(" summary=%q", summary)
	return line
}

func buildRevisionLine(id, sha string) string {
	return fmt.Sprintf("!revision id=%s sha=%s", id, sha)
}

func buildVoteLine(id, sha string, v Verdict) string {
	return fmt.Sprintf("!vote id=%s sha=%s verdict=%s", id, sha, v)
}

func buildExecutedLine(id, sha string) string {
	return fmt.Sprintf("!executed id=%s sha=%s", id, sha)
}

func checkLineLen(line string) error {
	if len(line) > maxBangLine {
		return fmt.Errorf("bang line is %d bytes (max %d) — shorten the summary", len(line), maxBangLine)
	}
	return nil
}

// bodyLines splits a review body into per-newline channel lines. Empty/blank
// bodies produce nothing. The client auto-splits any line over the byte limit.
func bodyLines(body string) []string {
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// shaMatches reports whether full begins with want (want may be abbreviated).
func shaMatches(full, want string) bool {
	return full == want || strings.HasPrefix(full, want)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
