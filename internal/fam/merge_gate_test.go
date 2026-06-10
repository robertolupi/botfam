package fam

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/rlupi/botfam/internal/store"
)

func TestMergeGate(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "botfam-merge-gate-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	st := store.New(tempDir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	// We set COLLAB_ROOT so Resolver resolves to our temp store
	t.Setenv("COLLAB_ROOT", tempDir)

	proposalID := "prop-1"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	// Case 1: No events at all
	var buf bytes.Buffer
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "no CCREP events found") {
		t.Errorf("expected no events error, got %v", err)
	}

	// Helper to send a ccrep message
	sendCcrep := func(from, to, typ string, payload map[string]any) {
		t.Helper()
		_, err := st.Send(from, to, typ, payload, "", nil)
		if err != nil {
			t.Fatalf("failed to send ccrep: %v", err)
		}
	}

	// Case 2: Proposed by alice, but no approvals
	sendCcrep("alice", "bob", "ccrep:proposal", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
	})

	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 3: Author (alice) approves herself
	sendCcrep("alice", "bob", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
		"verdict":     "approve",
	})
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 4: Independent approval by bob -> consensus!
	sendCcrep("bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err != nil {
		t.Errorf("expected consensus success, got error %v", err)
	}

	// Case 5: Independent critique / request changes from charlie -> blocked!
	sendCcrep("charlie", "alice", "ccrep:critique", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "charlie",
		"verdict":     "request_changes",
	})
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes") {
		t.Errorf("expected blocked error, got %v", err)
	}

	// Case 6: New revision proposed (commit2) -> approvals/verdicts for commit1 die/superseded
	// Send revision event
	sendCcrep("alice", "bob", "ccrep:revision", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// Try to check commit1 (the old one) -> should fail because it is superseded!
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit1, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "superseded by newer proposed commit") {
		t.Errorf("expected superseded error for old commit, got %v", err)
	}

	// Try to check commit2 (the new one) -> should fail because it has no approvals yet!
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit2, "--proposal", proposalID}, &buf)
	if err == nil || !strings.Contains(err.Error(), "has no independent approvals") {
		t.Errorf("expected no independent approvals error for new commit, got %v", err)
	}

	// Now bob approves commit2 -> success for commit2!
	sendCcrep("bob", "alice", "ccrep:evaluation", map[string]any{
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"reviewer":    "bob",
		"verdict":     "approve",
	})
	buf.Reset()
	err = MergeGateCmd([]string{"--commit", commit2, "--proposal", proposalID}, &buf)
	if err != nil {
		t.Errorf("expected consensus success for commit2, got error %v", err)
	}
}
