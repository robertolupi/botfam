package ccrep

import "context"

// Transport sends a single already-formatted line to the fam's coordination
// channel. Implementations: a writer to the live client's FIFO, a one-shot
// IRC dialer, or a fake for tests. The core never knows which.
type Transport interface {
	Send(ctx context.Context, line string) error
}

// Ledger is the read model: it folds the append-only history into proposal
// projections. It is read-only — writes happen via [Transport] and are
// observed back through the ledger.
type Ledger interface {
	// Tally returns the current projected state of one proposal. LatestSHA is
	// the commit that votes and merges bind to.
	Tally(ctx context.Context, proposalID string) (TallyResult, error)
	// ListProposals returns summary rows (for TUI/web listings).
	ListProposals(ctx context.Context, filter ProposalFilter) ([]ProposalView, error)
	// GetProposal returns one proposal summary.
	GetProposal(ctx context.Context, proposalID string) (ProposalView, error)
	// Subscribe streams ledger events as they arrive. The CLI ignores this; a
	// TUI/web adapter binds it to live UI updates. Closing ctx ends the stream.
	Subscribe(ctx context.Context) (<-chan Event, error)
}

// VersionControl is the version-control port. Implementations shell out to git;
// the same interface is meant to generalize to jj or hg. A fake is used in tests.
type VersionControl interface {
	// RevParse resolves a ref (e.g. "HEAD") to a full 40-char SHA.
	RevParse(ctx context.Context, ref string) (string, error)
	// IsPushed reports whether the commit is reachable on origin. A false
	// result is advisory only (see the origin-not-load-bearing note in Propose).
	IsPushed(ctx context.Context, sha string) (bool, error)
	// MainCheckout returns the path of the worktree that has main checked out,
	// where the executor merge runs.
	MainCheckout(ctx context.Context) (string, error)
	// MergeNoFF performs `git merge --no-ff <sha>` in dir and returns the new
	// HEAD (the merge commit).
	MergeNoFF(ctx context.Context, dir, sha, message string) (headSHA string, err error)
}
