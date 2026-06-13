// Package ccrep is the presentation-agnostic core of the ccrep coordination
// protocol (propose / revise / vote / tally / merge / gate).
//
// It is the "core" in a ports-and-adapters design: the CLI, the MCP server, and
// any future TUI or web UI are thin adapters over the [Engine] here. The core
// imports neither cobra, the MCP library, nor any IRC wire specifics — it
// depends only on the [Transport], [Ledger], and [Git] port interfaces, which
// each adapter wires up at its composition root.
//
// See doc/proposals/ccrep-mcp-tools.md for the design.
package ccrep

import (
	"os"
	"time"
)

// Verdict is a reviewer's decision on a proposal.
type Verdict string

const (
	// Approve counts toward quorum.
	Approve Verdict = "approve"
	// RequestChanges blocks the merge until a new revision.
	RequestChanges Verdict = "request_changes"
	// Comment is a non-binding note that carries no quorum weight.
	Comment Verdict = "comment"
)

func (v Verdict) valid() bool {
	switch v {
	case Approve, RequestChanges, Comment:
		return true
	}
	return false
}

// Quorum is the decision rule for a proposal.
type Quorum string

const (
	QuorumAll      Quorum = "all"
	QuorumMajority Quorum = "majority"
	QuorumAny      Quorum = "any"
)

func (q Quorum) valid() bool {
	switch q {
	case QuorumAll, QuorumMajority, QuorumAny:
		return true
	case "consensus":
		return os.Getenv("BOTFAM_SOCKET") != ""
	}
	return false
}

// Proposal status, as projected from the ledger by the read model.
const (
	StatusPending          = "pending"
	StatusApproved         = "approved"
	StatusChangesRequested = "changes_requested"
)

// --- command arguments -------------------------------------------------------

// ProposeArgs opens a new proposal. Ref defaults to HEAD; the SHA is resolved
// from it (never typed by the caller). Quorum defaults to QuorumMajority and
// Executor defaults to the engine's actor.
type ProposeArgs struct {
	ID       string
	Summary  string
	Quorum   Quorum
	Executor string
	Ref      string
	Deadline string
}

// ReviseArgs points an existing proposal at a new commit. Ref defaults to HEAD.
type ReviseArgs struct {
	ID  string
	Ref string
}

// VoteArgs casts a vote. The SHA is resolved from the proposal's current
// revision in the ledger, not from the caller. Expect, if set, asserts the
// proposal is still at that (possibly abbreviated) SHA and fails otherwise.
type VoteArgs struct {
	ID      string
	Verdict Verdict
	Body    string
	Expect  string
}

// TallyArgs reads the current state of a proposal.
type TallyArgs struct {
	ID string
}

// MergeArgs executes an approved proposal (the executor step).
type MergeArgs struct {
	ID string
}

// GateArgs checks whether a proposal/commit is clear to merge.
type GateArgs struct {
	ID     string
	Commit string
}

// --- results (presentation-neutral, JSON-serialisable) -----------------------

// ActionResult is returned by the write verbs (propose/revise/vote). Line is
// the exact bang line emitted; Warnings are non-fatal advisories for the
// adapter to surface (e.g. "not reachable on origin").
type ActionResult struct {
	ProposalID string   `json:"proposal_id"`
	SHA        string   `json:"sha"`
	Line       string   `json:"line"`
	Warnings   []string `json:"warnings,omitempty"`
}

// MergeResult reports an executed merge. MergedSHA is the approved commit;
// HeadSHA is the resulting merge commit on main.
type MergeResult struct {
	ProposalID string `json:"proposal_id"`
	MergedSHA  string `json:"merged_sha"`
	HeadSHA    string `json:"head_sha"`
	Line       string `json:"line"`
}

// GateResult reports a merge-gate decision.
type GateResult struct {
	ProposalID string `json:"proposal_id"`
	Passed     bool   `json:"passed"`
	Approvals  int    `json:"approvals"`
	SHA        string `json:"sha"`
	Reason     string `json:"reason"`
}

// Vote is one reviewer's recorded vote, as projected from the ledger.
type Vote struct {
	Actor   string  `json:"actor"`
	Verdict Verdict `json:"verdict"`
	SHA     string  `json:"sha"`
	Present bool    `json:"present"`
}

// TallyResult is the read-model projection of a proposal's current state.
// LatestSHA is the commit of the most recent revision and is the SHA that
// votes and merges bind to.
type TallyResult struct {
	ProposalID string   `json:"proposal_id"`
	LatestSHA  string   `json:"latest_sha"`
	Status     string   `json:"status"`
	Quorum     Quorum   `json:"quorum"`
	Author     string   `json:"author"`
	Approvals  []string `json:"approvals"`
	Votes      []Vote   `json:"votes"`
}

// ProposalView is a summary row for listings (used by TUI/web adapters).
type ProposalView struct {
	ID        string `json:"id"`
	LatestSHA string `json:"latest_sha"`
	Status    string `json:"status"`
	Author    string `json:"author"`
	Quorum    Quorum `json:"quorum"`
	Summary   string `json:"summary"`
}

// ProposalFilter narrows a ListProposals query.
type ProposalFilter struct {
	OpenOnly bool
}

// EventKind classifies a ledger event for the reactive read stream.
type EventKind string

const (
	EventProposed EventKind = "proposed"
	EventRevised  EventKind = "revised"
	EventVoted    EventKind = "voted"
	EventExecuted EventKind = "executed"
)

// Event is one item on the [Ledger.Subscribe] stream. A TUI/web adapter turns
// these into UI updates; the CLI ignores the stream.
type Event struct {
	Kind       EventKind `json:"kind"`
	ProposalID string    `json:"proposal_id"`
	SHA        string    `json:"sha"`
	Actor      string    `json:"actor"`
	Verdict    Verdict   `json:"verdict,omitempty"`
	TS         time.Time `json:"ts"`
}
