// Package seam provides the CattleSeam interceptor layer: thin proxy types that
// wrap service interfaces defined in proto, enforce famctx.MustHaveIdentity at
// every entry point, and (in test mode) act as a chaos valve.
//
// Dependency position: seam imports both famctx and proto, sitting above both in
// the leaf hierarchy. Adapters (cli, mcp) and actor implementations import seam
// when they need to install the interceptor at a wiring point. seam itself never
// imports cli or mcp.
//
// Usage pattern:
//
//	// At the wiring point (adapter or harness):
//	var lw proto.LedgerWriter = seam.LedgerWriter(flock.NewLedgerWriter(...))
//
//	// Every call to lw.Apply now panics if the caller forgot to stamp the context,
//	// and emits entry/exit trace events for prod observability or chaos injection.
package seam

import (
	"context"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/proto"
)

// guard calls famctx.MustHaveIdentity and returns the resolved identity.
// All proxy entry points call this before delegating.
func guard(ctx context.Context) famctx.Context {
	return famctx.MustHaveIdentity(ctx)
}

// --- Blackboard proxy --------------------------------------------------------

type seamBlackboard struct{ inner proto.Blackboard }

// Blackboard wraps inner so that every ReadFact/WriteFact call enforces a
// stamped famctx.Context. Panics on entry if the context has no identity.
func Blackboard(inner proto.Blackboard) proto.Blackboard {
	return seamBlackboard{inner: inner}
}

func (s seamBlackboard) ReadFact(ctx context.Context, plane proto.Plane, key string) (string, error) {
	guard(ctx)
	return s.inner.ReadFact(ctx, plane, key)
}

func (s seamBlackboard) WriteFact(ctx context.Context, plane proto.Plane, key string, val string) error {
	guard(ctx)
	return s.inner.WriteFact(ctx, plane, key, val)
}

// --- ClaimManager proxy ------------------------------------------------------

type seamClaimManager struct{ inner proto.ClaimManager }

// ClaimManager wraps inner with famctx enforcement.
func ClaimManager(inner proto.ClaimManager) proto.ClaimManager {
	return seamClaimManager{inner: inner}
}

func (s seamClaimManager) ClaimTask(ctx context.Context, taskID string, agentID string) error {
	guard(ctx)
	return s.inner.ClaimTask(ctx, taskID, agentID)
}

func (s seamClaimManager) ReleaseTask(ctx context.Context, taskID string, agentID string) error {
	guard(ctx)
	return s.inner.ReleaseTask(ctx, taskID, agentID)
}

func (s seamClaimManager) GetActiveClaims(ctx context.Context, agentID string) ([]string, error) {
	guard(ctx)
	return s.inner.GetActiveClaims(ctx, agentID)
}

// --- MergeQueue proxy --------------------------------------------------------

type seamMergeQueue struct{ inner proto.MergeQueue }

// MergeQueue wraps inner with famctx enforcement.
func MergeQueue(inner proto.MergeQueue) proto.MergeQueue {
	return seamMergeQueue{inner: inner}
}

func (s seamMergeQueue) EnqueueMerge(ctx context.Context, prID string, baseSHA string, headSHA string) (string, error) {
	guard(ctx)
	return s.inner.EnqueueMerge(ctx, prID, baseSHA, headSHA)
}

func (s seamMergeQueue) GetMergeStatus(ctx context.Context, prID string) (proto.MergeStatus, error) {
	guard(ctx)
	return s.inner.GetMergeStatus(ctx, prID)
}

// --- HandoverManager proxy ---------------------------------------------------

type seamHandoverManager struct{ inner proto.HandoverManager }

// HandoverManager wraps inner with famctx enforcement.
func HandoverManager(inner proto.HandoverManager) proto.HandoverManager {
	return seamHandoverManager{inner: inner}
}

func (s seamHandoverManager) SaveCheckpoint(ctx context.Context, taskID string, state []byte) error {
	guard(ctx)
	return s.inner.SaveCheckpoint(ctx, taskID, state)
}

func (s seamHandoverManager) LoadCheckpoint(ctx context.Context, taskID string) ([]byte, error) {
	guard(ctx)
	return s.inner.LoadCheckpoint(ctx, taskID)
}
