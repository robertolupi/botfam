package ccrep

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- fakes -------------------------------------------------------------------

type fakeGit struct {
	head       string
	pushed     bool
	pushErr    error
	mainDir    string
	mergeHead  string
	mergeErr   error
	mergedSHA  string // captured arg
	mergedDir  string // captured arg
	revParseFn func(ref string) (string, error)
}

func (g *fakeGit) RevParse(_ context.Context, ref string) (string, error) {
	if g.revParseFn != nil {
		return g.revParseFn(ref)
	}
	return g.head, nil
}
func (g *fakeGit) IsPushed(_ context.Context, _ string) (bool, error) {
	return g.pushed, g.pushErr
}
func (g *fakeGit) MainCheckout(_ context.Context) (string, error) {
	if g.mainDir == "" {
		return "", errors.New("no main checkout")
	}
	return g.mainDir, nil
}
func (g *fakeGit) MergeNoFF(_ context.Context, dir, sha, _ string) (string, error) {
	g.mergedDir, g.mergedSHA = dir, sha
	return g.mergeHead, g.mergeErr
}

type fakeLedger struct {
	tally    TallyResult
	tallyErr error
}

func (l *fakeLedger) Tally(_ context.Context, _ string) (TallyResult, error) {
	return l.tally, l.tallyErr
}
func (l *fakeLedger) ListProposals(_ context.Context, _ ProposalFilter) ([]ProposalView, error) {
	return nil, nil
}
func (l *fakeLedger) GetProposal(_ context.Context, _ string) (ProposalView, error) {
	return ProposalView{}, nil
}
func (l *fakeLedger) Subscribe(_ context.Context) (<-chan Event, error) {
	ch := make(chan Event)
	close(ch)
	return ch, nil
}

type fakeTransport struct {
	sent    []string
	sendErr error
}

func (t *fakeTransport) Send(_ context.Context, line string) error {
	if t.sendErr != nil {
		return t.sendErr
	}
	t.sent = append(t.sent, line)
	return nil
}

const (
	fullSHA  = "e627467f11442d2fe19a8b98a92ecd80684aa55f"
	otherSHA = "1a94f8200000000000000000000000000000000a"
)

func newEngine(g *fakeGit, l *fakeLedger, t *fakeTransport) *Engine {
	return New(g, l, t, "claude", QuorumMajority)
}

// --- propose -----------------------------------------------------------------

func TestPropose_ComputesSHAAndDefaults(t *testing.T) {
	g := &fakeGit{head: fullSHA, pushed: true}
	tx := &fakeTransport{}
	e := newEngine(g, &fakeLedger{}, tx)

	res, err := e.Propose(context.Background(), ProposeArgs{ID: "p1", Summary: "do a thing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA != fullSHA {
		t.Errorf("SHA = %q, want %q", res.SHA, fullSHA)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
	want := `!propose id=p1 sha=` + fullSHA + ` quorum=majority executor=claude summary="do a thing"`
	if got := only(t, tx); got != want {
		t.Errorf("line =\n  %q\nwant\n  %q", got, want)
	}
}

func TestPropose_NotPushedWarnsButSends(t *testing.T) {
	g := &fakeGit{head: fullSHA, pushed: false}
	tx := &fakeTransport{}
	e := newEngine(g, &fakeLedger{}, tx)

	res, err := e.Propose(context.Background(), ProposeArgs{ID: "p1", Summary: "x", Quorum: QuorumAny})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "origin") {
		t.Errorf("expected an origin warning, got %v", res.Warnings)
	}
	if len(tx.sent) != 1 {
		t.Errorf("expected the proposal to still be sent, got %d lines", len(tx.sent))
	}
}

func TestPropose_RejectsBadQuorum(t *testing.T) {
	e := newEngine(&fakeGit{head: fullSHA}, &fakeLedger{}, &fakeTransport{})
	if _, err := e.Propose(context.Background(), ProposeArgs{ID: "p", Summary: "s", Quorum: "consensus"}); err == nil {
		t.Fatal("expected error for invalid quorum")
	}
}

// --- vote --------------------------------------------------------------------

func TestVote_BindsToLedgerSHANotHEAD(t *testing.T) {
	// git HEAD is otherSHA, but the proposal's current revision is fullSHA.
	g := &fakeGit{head: otherSHA}
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA}}
	tx := &fakeTransport{}
	e := newEngine(g, l, tx)

	res, err := e.Vote(context.Background(), VoteArgs{ID: "p1", Verdict: Approve})
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA != fullSHA {
		t.Errorf("voted on %q, want ledger SHA %q", res.SHA, fullSHA)
	}
	want := "!vote id=p1 sha=" + fullSHA + " verdict=approve"
	if got := only(t, tx); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
}

func TestVote_ExpectMismatchFailsAndSendsNothing(t *testing.T) {
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA}}
	tx := &fakeTransport{}
	e := newEngine(&fakeGit{}, l, tx)

	_, err := e.Vote(context.Background(), VoteArgs{ID: "p1", Verdict: Approve, Expect: "1a94f82"})
	if err == nil {
		t.Fatal("expected --expect mismatch error")
	}
	if len(tx.sent) != 0 {
		t.Errorf("nothing should be sent on mismatch, got %v", tx.sent)
	}
}

func TestVote_ExpectMatchPrefix(t *testing.T) {
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA}}
	e := newEngine(&fakeGit{}, l, &fakeTransport{})
	if _, err := e.Vote(context.Background(), VoteArgs{ID: "p1", Verdict: Approve, Expect: "e627467"}); err != nil {
		t.Fatalf("abbreviated --expect that matches should pass: %v", err)
	}
}

func TestVote_PostsBodyAsFollowOnLines(t *testing.T) {
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA}}
	tx := &fakeTransport{}
	e := newEngine(&fakeGit{}, l, tx)

	_, err := e.Vote(context.Background(), VoteArgs{
		ID: "p1", Verdict: RequestChanges, Body: "blocking: X\n\nnit: Y\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	// bang line + 3 body lines ("blocking: X", "", "nit: Y")
	if len(tx.sent) != 4 {
		t.Fatalf("expected 1 bang + 3 body lines, got %d: %v", len(tx.sent), tx.sent)
	}
	if !strings.HasPrefix(tx.sent[0], "!vote ") {
		t.Errorf("first line should be the bang line, got %q", tx.sent[0])
	}
	if tx.sent[1] != "blocking: X" || tx.sent[3] != "nit: Y" {
		t.Errorf("body lines wrong: %v", tx.sent[1:])
	}
}

func TestVote_InvalidVerdict(t *testing.T) {
	e := newEngine(&fakeGit{}, &fakeLedger{tally: TallyResult{LatestSHA: fullSHA}}, &fakeTransport{})
	if _, err := e.Vote(context.Background(), VoteArgs{ID: "p1", Verdict: "lgtm"}); err == nil {
		t.Fatal("expected invalid-verdict error")
	}
}

func TestVote_NoCurrentCommit(t *testing.T) {
	e := newEngine(&fakeGit{}, &fakeLedger{tally: TallyResult{}}, &fakeTransport{})
	if _, err := e.Vote(context.Background(), VoteArgs{ID: "p1", Verdict: Approve}); err == nil {
		t.Fatal("expected error when proposal has no current commit")
	}
}

// --- merge / gate ------------------------------------------------------------

func TestMerge_ApprovedMergesAndAnnounces(t *testing.T) {
	mergeHead := "abc1230000000000000000000000000000000def"
	g := &fakeGit{mainDir: "/repo/main", mergeHead: mergeHead}
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA, Status: StatusApproved, Approvals: []string{"rlupi"}}}
	tx := &fakeTransport{}
	e := newEngine(g, l, tx)

	res, err := e.Merge(context.Background(), MergeArgs{ID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if g.mergedSHA != fullSHA || g.mergedDir != "/repo/main" {
		t.Errorf("merged (dir=%q sha=%q), want (/repo/main, %s)", g.mergedDir, g.mergedSHA, fullSHA)
	}
	if res.HeadSHA != mergeHead {
		t.Errorf("HeadSHA = %q, want %q", res.HeadSHA, mergeHead)
	}
	want := "!executed id=p1 sha=" + mergeHead
	if got := only(t, tx); got != want {
		t.Errorf("announce = %q, want %q", got, want)
	}
}

func TestMerge_NotApprovedDoesNotMerge(t *testing.T) {
	g := &fakeGit{mainDir: "/repo/main", mergeHead: "x"}
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA, Status: StatusPending}}
	tx := &fakeTransport{}
	e := newEngine(g, l, tx)

	if _, err := e.Merge(context.Background(), MergeArgs{ID: "p1"}); err == nil {
		t.Fatal("expected error merging an unapproved proposal")
	}
	if g.mergedSHA != "" {
		t.Error("must not call git merge when not approved")
	}
	if len(tx.sent) != 0 {
		t.Error("must not announce when not approved")
	}
}

func TestGate_PassAndFail(t *testing.T) {
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA, Status: StatusApproved, Approvals: []string{"rlupi"}}}
	e := newEngine(&fakeGit{}, l, &fakeTransport{})

	pass, err := e.Gate(context.Background(), GateArgs{ID: "p1", Commit: "e627467"})
	if err != nil {
		t.Fatal(err)
	}
	if !pass.Passed || pass.Approvals != 1 {
		t.Errorf("expected pass with 1 approval, got %+v", pass)
	}

	l.tally.Status = StatusChangesRequested
	fail, _ := e.Gate(context.Background(), GateArgs{ID: "p1"})
	if fail.Passed {
		t.Error("expected gate to fail when changes requested")
	}
}

func TestGate_WrongCommitFails(t *testing.T) {
	l := &fakeLedger{tally: TallyResult{LatestSHA: fullSHA, Status: StatusApproved}}
	e := newEngine(&fakeGit{}, l, &fakeTransport{})
	res, _ := e.Gate(context.Background(), GateArgs{ID: "p1", Commit: "deadbee"})
	if res.Passed {
		t.Error("gate should fail when the named commit is not the current revision")
	}
}

// --- helpers -----------------------------------------------------------------

// only returns the single line the transport sent, failing otherwise.
func only(t *testing.T, tx *fakeTransport) string {
	t.Helper()
	if len(tx.sent) != 1 {
		t.Fatalf("expected exactly 1 line, got %d: %v", len(tx.sent), tx.sent)
	}
	return tx.sent[0]
}
