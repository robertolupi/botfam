package fam

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rlupi/botfam/internal/store"
)

// setupMergeGateStore creates a temp store and points COLLAB_ROOT at it so
// MergeGateCmd's Resolver picks it up (Resolver Env is nil; t.Setenv works).
func setupMergeGateStore(t *testing.T) store.Store {
	t.Helper()
	tempDir := t.TempDir()
	st := store.New(tempDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLLAB_ROOT", tempDir)
	return st
}

// sendCcrep sends a ccrep message from one actor to another. It sleeps briefly
// so consecutive events get strictly increasing timestamps (the gate reduces
// to the latest verdict per reviewer by timestamp).
func sendCcrep(t *testing.T, st store.Store, from, to, typ string, payload map[string]any) {
	t.Helper()
	if _, err := st.Send(from, to, typ, payload, "", nil); err != nil {
		t.Fatalf("failed to send ccrep: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
}

func runMergeGate(t *testing.T, commit, proposal string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := MergeGateCmd([]string{"--commit", commit, "--proposal", proposal}, &buf)
	return buf.String(), err
}

func TestMergeGate(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-1"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	// Case 1: No events at all
	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "no CCREP events found") {
		t.Errorf("expected no events error, got %v", err)
	}

	// Case 2: Proposed by alice, but no approvals
	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
	})

	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 3: Author (alice) approves herself
	sendCcrep(t, st, "alice", "bob", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
		"verdict":     "approve",
	})
	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 4: Independent approval by bob -> consensus!
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})
	_, err = runMergeGate(t, commit1, proposalID)
	if err != nil {
		t.Errorf("expected consensus success, got error %v", err)
	}

	// Case 5: Independent critique / request changes from charlie -> blocked!
	sendCcrep(t, st, "charlie", "alice", "ccrep:critique", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "charlie",
		"verdict":     "request_changes",
	})
	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes") {
		t.Errorf("expected blocked error, got %v", err)
	}

	// Case 6: New revision proposed (commit2) -> approvals for commit1 die/superseded
	sendCcrep(t, st, "alice", "bob", "ccrep:revision", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// Try to check commit1 (the old one) -> should fail because it is superseded!
	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "superseded by newer proposed commit") {
		t.Errorf("expected superseded error for old commit, got %v", err)
	}

	// Try to check commit2 (the new one) -> should fail because it has no approvals yet!
	_, err = runMergeGate(t, commit2, proposalID)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error for new commit, got %v", err)
	}

	// Bob approves commit2, but charlie's request_changes on commit1 is still
	// his latest verdict -> the critique persists across revisions and blocks.
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"reviewer":    "bob",
		"verdict":     "approve",
	})
	_, err = runMergeGate(t, commit2, proposalID)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes") {
		t.Errorf("expected blocked error from persistent critique, got %v", err)
	}

	// Charlie approves commit2, clearing his own earlier critique -> success.
	sendCcrep(t, st, "charlie", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"reviewer":    "charlie",
		"verdict":     "approve",
	})
	_, err = runMergeGate(t, commit2, proposalID)
	if err != nil {
		t.Errorf("expected consensus success for commit2, got error %v", err)
	}
}

func TestMergeGateSpoofedReviewer(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-spoof"
	commit1 := "commit-1111111111111111111111111111111111111111"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	// Author alice forges an "approval by bob" from her own mailbox.
	sendCcrep(t, st, "alice", "bob", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})

	out, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected spoofed approval to be rejected, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "spoof-suspect") {
		t.Errorf("expected error to mention ignored spoof-suspect events, got %v", err)
	}
	if !strings.Contains(out, "spoof-suspect") {
		t.Errorf("expected gate output to flag the spoof-suspect event, got %q", out)
	}
}

func TestMergeGateApprovalWithoutPayloadReviewer(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-fallback"
	commit1 := "commit-1111111111111111111111111111111111111111"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	// Bob approves without a payload reviewer field: identity falls back to
	// the authenticated msg.From.
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})

	if _, err := runMergeGate(t, commit1, proposalID); err != nil {
		t.Errorf("expected consensus success via msg.From fallback, got %v", err)
	}
}

func TestMergeGateCritiquePersistsAcrossRevisions(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-persist"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	sendCcrep(t, st, "charlie", "alice", "ccrep:critique", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "request_changes",
	})
	sendCcrep(t, st, "alice", "bob", "ccrep:revision", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// A different reviewer approves the new commit, but charlie's blocking
	// verdict on commit1 is still his latest word -> gate must block.
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"verdict":     "approve",
	})

	_, err := runMergeGate(t, commit2, proposalID)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes") {
		t.Errorf("expected persistent critique to block commit2, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "charlie") {
		t.Errorf("expected blocker to name charlie, got %v", err)
	}
}

func TestMergeGateReviewerClearsOwnCritique(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-clear"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	sendCcrep(t, st, "bob", "alice", "ccrep:critique", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "request_changes",
	})
	sendCcrep(t, st, "alice", "bob", "ccrep:revision", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// Bob's later approval of commit2 supersedes his own request_changes.
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"verdict":     "approve",
	})

	if _, err := runMergeGate(t, commit2, proposalID); err != nil {
		t.Errorf("expected reviewer's own approval to clear their critique, got %v", err)
	}
}

func TestMergeGateRejectBlocks(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-reject"
	commit1 := "commit-1111111111111111111111111111111111111111"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})
	sendCcrep(t, st, "charlie", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "reject",
	})

	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes/reject") {
		t.Errorf("expected reject verdict to block, got %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "reject") {
		t.Errorf("expected blocker to carry the reject verdict, got %v", err)
	}
}

func TestMergeGateMissingProposalFailsClosed(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-noprop"
	commit1 := "commit-1111111111111111111111111111111111111111"

	// An approval exists but no ccrep:proposal event: the gate must refuse
	// instead of treating the approver as independent of an unknown author.
	sendCcrep(t, st, "bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})

	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "no ccrep:proposal event found") {
		t.Errorf("expected fail-closed error for missing proposal event, got %v", err)
	}
}

func TestMergeGateRevisionWithoutCommitSHA(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-badrev"
	commit1 := "commit-1111111111111111111111111111111111111111"

	sendCcrep(t, st, "alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	sendCcrep(t, st, "alice", "bob", "ccrep:revision", map[string]any{
		"proposal_id": proposalID,
	})

	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "missing commit_sha") {
		t.Errorf("expected invalid revision error, got %v", err)
	}
}

func TestMergeGateSessionSourcedEvents(t *testing.T) {
	st := setupMergeGateStore(t)

	proposalID := "prop-session"
	commit1 := "commit-1111111111111111111111111111111111111111"

	if err := st.SessionNew(proposalID, []string{"alice", "bob"}, "alice", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	aliceLock, err := st.LockActor("alice")
	if err != nil {
		t.Fatal(err)
	}
	defer aliceLock.Close()
	bobLock, err := st.LockActor("bob")
	if err != nil {
		t.Fatal(err)
	}
	defer bobLock.Close()

	appendCcrep := func(actor string, body map[string]any) {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.SessionAppend(proposalID, actor, string(b), nil); err != nil {
			t.Fatalf("failed to append session entry: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	appendCcrep("alice", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	// Alice forges an approval-by-bob inside the session log: the reviewer is
	// the entry's actor, so this is a spoof suspect and must not count.
	appendCcrep("alice", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})

	out, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected spoofed session approval to be rejected, got %v", err)
	}
	if !strings.Contains(out, "spoof-suspect") {
		t.Errorf("expected gate output to flag the session spoof-suspect event, got %q", out)
	}

	// Bob's own session entry (actor == bob, no payload reviewer) counts.
	appendCcrep("bob", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})

	out, err = runMergeGate(t, commit1, proposalID)
	if err != nil {
		t.Errorf("expected consensus success from session-sourced events, got %v", err)
	}
	if !strings.Contains(out, "Consensus reached") {
		t.Errorf("expected consensus output, got %q", out)
	}
}
