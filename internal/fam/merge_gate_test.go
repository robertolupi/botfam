package fam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupMergeGateHistory creates a temp history.jsonl file and sets COLLAB_HISTORY env var.
func setupMergeGateHistory(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	historyPath := filepath.Join(tempDir, "history.jsonl")
	
	// Create empty file
	f, err := os.Create(historyPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	t.Setenv("COLLAB_HISTORY", historyPath)
	return historyPath
}

// appendHistoryEntry writes a structured history log line to the history file.
func appendHistoryEntry(t *testing.T, path, sender, msgType string, payload map[string]any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	entry := struct {
		Timestamp string `json:"timestamp"`
		Sender    string `json:"sender"`
		Type      string `json:"type"`
		Target    string `json:"target"`
		Body      string `json:"body"`
	}{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Sender:    sender,
		Type:      "PRIVMSG",
		Target:    "#botfam",
		Body:      string(payloadBytes),
	}

	bytes, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.Write(append(bytes, '\n'))
	time.Sleep(2 * time.Millisecond) // strictly increasing timestamps
}

func runMergeGate(t *testing.T, commit, proposal string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := MergeGateCmd([]string{"--commit", commit, "--proposal", proposal}, &buf)
	return buf.String(), err
}

func TestMergeGate(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-1"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	// Case 1: No events at all
	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "no CCREP events found") {
		t.Errorf("expected no events error, got %v", err)
	}

	// Case 2: Proposed by alice, but no approvals
	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
	})

	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "independent approval") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 3: Author (alice) approves herself
	appendHistoryEntry(t, path, "alice", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
		"verdict":     "approve",
	})
	_, err = runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "independent approval") {
		t.Errorf("expected no independent approvals error, got %v", err)
	}

	// Case 4: Independent approval by bob -> consensus!
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
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
	appendHistoryEntry(t, path, "charlie", "ccrep:critique", map[string]any{
		"type":        "ccrep:critique",
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
	appendHistoryEntry(t, path, "alice", "ccrep:revision", map[string]any{
		"type":        "ccrep:revision",
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
	if err == nil || !strings.Contains(err.Error(), "independent approval") {
		t.Errorf("expected no independent approvals error for new commit, got %v", err)
	}

	// Bob approves commit2, but charlie's request_changes on commit1 is still
	// his latest verdict -> the critique persists across revisions and blocks.
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
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
	appendHistoryEntry(t, path, "charlie", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
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
	path := setupMergeGateHistory(t)

	proposalID := "prop-spoof"
	commit1 := "commit-1111111111111111111111111111111111111111"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	// Author alice forges an "approval by bob".
	appendHistoryEntry(t, path, "alice", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})

	out, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "independent approval") {
		t.Errorf("expected spoofed approval to be rejected, got %v", err)
	}
	if !strings.Contains(out, "spoof-suspect") {
		t.Errorf("expected gate output to flag the spoof-suspect event, got %q", out)
	}
}

func TestMergeGateApprovalWithoutPayloadReviewer(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-fallback"
	commit1 := "commit-1111111111111111111111111111111111111111"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	// Bob approves without a payload reviewer field: identity falls back to
	// the authenticated sender.
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})

	if _, err := runMergeGate(t, commit1, proposalID); err != nil {
		t.Errorf("expected consensus success via msg.From fallback, got %v", err)
	}
}

func TestMergeGateCritiquePersistsAcrossRevisions(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-persist"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	appendHistoryEntry(t, path, "charlie", "ccrep:critique", map[string]any{
		"type":        "ccrep:critique",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "request_changes",
	})
	appendHistoryEntry(t, path, "alice", "ccrep:revision", map[string]any{
		"type":        "ccrep:revision",
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// A different reviewer approves the new commit, but charlie's blocking
	// verdict on commit1 is still his latest word -> gate must block.
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
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
	path := setupMergeGateHistory(t)

	proposalID := "prop-clear"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	appendHistoryEntry(t, path, "bob", "ccrep:critique", map[string]any{
		"type":        "ccrep:critique",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "request_changes",
	})
	appendHistoryEntry(t, path, "alice", "ccrep:revision", map[string]any{
		"type":        "ccrep:revision",
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	// Bob's later approval of commit2 supersedes his own request_changes.
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit2,
		"verdict":     "approve",
	})

	if _, err := runMergeGate(t, commit2, proposalID); err != nil {
		t.Errorf("expected reviewer's own approval to clear their critique, got %v", err)
	}
}

func TestMergeGateRejectBlocks(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-reject"
	commit1 := "commit-1111111111111111111111111111111111111111"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "approve",
	})
	appendHistoryEntry(t, path, "charlie", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"verdict":     "reject",
	})

	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "is blocked by request_changes/reject") {
		t.Errorf("expected reject verdict to block, got %v", err)
	}
}

func TestMergeGateMissingProposalFailsClosed(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-noprop"
	commit1 := "commit-1111111111111111111111111111111111111111"

	// An approval exists but no ccrep:proposal event.
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
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
	path := setupMergeGateHistory(t)

	proposalID := "prop-badrev"
	commit1 := "commit-1111111111111111111111111111111111111111"

	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
	})
	appendHistoryEntry(t, path, "alice", "ccrep:revision", map[string]any{
		"type":        "ccrep:revision",
		"proposal_id": proposalID,
	})

	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "missing commit_sha") {
		t.Errorf("expected invalid revision error, got %v", err)
	}
}

func TestTallyProposal(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-tally"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	// Case 1: Empty file
	summary, err := TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "no events found") {
		t.Errorf("expected no events warning, got %q", summary)
	}

	// Case 2: Proposed by alice, no votes
	appendHistoryEntry(t, path, "alice", "ccrep:proposal", map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: PENDING") || !strings.Contains(summary, "approvals: none") {
		t.Errorf("expected pending status with no approvals, got %q", summary)
	}

	// Case 3: Author approval (should not count as independent approval)
	appendHistoryEntry(t, path, "alice", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "alice",
		"verdict":     "approve",
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: PENDING") || !strings.Contains(summary, "approvals: none") {
		t.Errorf("expected pending status with author approval only, got %q", summary)
	}

	// Case 4: Independent approval by bob -> APPROVED
	appendHistoryEntry(t, path, "bob", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "bob",
		"verdict":     "approve",
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") || !strings.Contains(summary, "approvals: bob") {
		t.Errorf("expected approved status with bob, got %q", summary)
	}

	// Case 5: Critique by charlie -> BLOCKED
	appendHistoryEntry(t, path, "charlie", "ccrep:critique", map[string]any{
		"type":        "ccrep:critique",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "charlie",
		"verdict":     "request_changes",
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: BLOCKED") || !strings.Contains(summary, "blockers: charlie") {
		t.Errorf("expected blocked status with charlie, got %q", summary)
	}

	// Case 6: Spoof vote -> should be ignored/counted in ignored_spoof_suspects
	appendHistoryEntry(t, path, "alice", "ccrep:evaluation", map[string]any{
		"type":        "ccrep:evaluation",
		"proposal_id": proposalID,
		"commit_sha":  commit1,
		"reviewer":    "david", // alice claims reviewer is david
		"verdict":     "approve",
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "ignored_spoof_suspects: 1") {
		t.Errorf("expected 1 ignored spoof suspect, got %q", summary)
	}

	// Case 7: Revision to commit2 -> old approvals die, but persistent critique blocks
	appendHistoryEntry(t, path, "alice", "ccrep:revision", map[string]any{
		"type":        "ccrep:revision",
		"proposal_id": proposalID,
		"commit_sha":  commit2,
	})
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: BLOCKED") || !strings.Contains(summary, "commit: commit-2222") || !strings.Contains(summary, "approvals: none") {
		t.Errorf("expected blocked on commit2 with no approvals, got %q", summary)
	}
}

func appendRawHistoryEntry(t *testing.T, path, sender, body string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	entry := struct {
		Timestamp string `json:"timestamp"`
		Sender    string `json:"sender"`
		Type      string `json:"type"`
		Target    string `json:"target"`
		Body      string `json:"body"`
	}{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Sender:    sender,
		Type:      "PRIVMSG",
		Target:    "#botfam",
		Body:      body,
	}

	bytes, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}

	_, _ = f.Write(append(bytes, '\n'))
	time.Sleep(2 * time.Millisecond)
}

func TestMergeGateBangVerbs(t *testing.T) {
	path := setupMergeGateHistory(t)

	proposalID := "prop-bang"
	commit1 := "commit-1111111111111111111111111111111111111111"
	commit2 := "commit-2222222222222222222222222222222222222222"

	// 1. Propose via bang line
	appendRawHistoryEntry(t, path, "alice", fmt.Sprintf("!propose id=%s sha=%s quorum=any deadline=2030-06-11T12:00:00+02:00 summary=Test proposal with spaces", proposalID, commit1))

	// Verify tally is pending
	summary, err := TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: PENDING") || !strings.Contains(summary, "commit: commit-1111") {
		t.Errorf("expected pending status, got %q", summary)
	}

	// 2. Evaluate/Approve via bang line
	appendRawHistoryEntry(t, path, "bob", fmt.Sprintf("!evaluate id=%s sha=%s verdict=approve evidence=Looks good", proposalID, commit1))

	// Verify tally shows approved
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") || !strings.Contains(summary, "approvals: bob") {
		t.Errorf("expected approved status by bob, got %q", summary)
	}

	// 3. Critique/Block via bang line
	appendRawHistoryEntry(t, path, "charlie", fmt.Sprintf("!evaluate id=%s sha=%s verdict=request_changes evidence=Found a bug", proposalID, commit1))

	// Verify tally is blocked
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: BLOCKED") || !strings.Contains(summary, "blockers: charlie") {
		t.Errorf("expected blocked status, got %q", summary)
	}

	// 4. Revision via bang line
	appendRawHistoryEntry(t, path, "alice", fmt.Sprintf("!revision id=%s sha=%s", proposalID, commit2))

	// Verify tally is still blocked but target commit has updated
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: BLOCKED") || !strings.Contains(summary, "commit: commit-2222") {
		t.Errorf("expected blocked status on commit2, got %q", summary)
	}

	// 5. Vote/Approve via bang line
	appendRawHistoryEntry(t, path, "charlie", fmt.Sprintf("!vote id=%s sha=%s verdict=approve", proposalID, commit2))

	// Verify tally is approved (charlie's vote clears their blocker and counts as an independent approval)
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") || !strings.Contains(summary, "approvals: charlie") {
		t.Errorf("expected approved status after blocker cleared, got %q", summary)
	}

	// 6. Bob approves commit2 via vote bang line
	appendRawHistoryEntry(t, path, "bob", fmt.Sprintf("!vote id=%s sha=%s verdict=approve", proposalID, commit2))

	// Verify tally is approved with both approvals listed
	summary, err = TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: APPROVED") || !strings.Contains(summary, "approvals: bob, charlie") {
		t.Errorf("expected approved status by bob and charlie, got %q", summary)
	}
}

func TestRequiredIndependentApprovals(t *testing.T) {
	roster := []string{"alice", "bob", "charlie"}
	
	// quorumType: all, author in roster
	if got := requiredIndependentApprovals("all", roster, "alice"); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
	// quorumType: all, author not in roster
	if got := requiredIndependentApprovals("all", roster, "david"); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	// quorumType: majority, author in roster
	if got := requiredIndependentApprovals("majority", roster, "alice"); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
	// quorumType: majority, author not in roster
	if got := requiredIndependentApprovals("majority", roster, "david"); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
	// quorumType: any, author in roster
	if got := requiredIndependentApprovals("any", roster, "alice"); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
	// empty roster
	if got := requiredIndependentApprovals("all", nil, "alice"); got != 1 {
		t.Errorf("expected 1, got %d", got)
	}
}

func TestMergeGateDeadline(t *testing.T) {
	path := setupMergeGateHistory(t)
	proposalID := "prop-deadline"
	commit1 := "commit-1111111111111111111111111111111111111111"

	// Propose with a deadline in the past
	pastDeadline := time.Now().Add(-time.Hour).Format(time.RFC3339)
	appendRawHistoryEntry(t, path, "alice", fmt.Sprintf("!propose id=%s sha=%s quorum=any deadline=%s summary=Past deadline test", proposalID, commit1, pastDeadline))

	// Evaluate (approve)
	appendRawHistoryEntry(t, path, "bob", fmt.Sprintf("!vote id=%s sha=%s verdict=approve", proposalID, commit1))

	// Merge gate should fail because it has expired
	_, err := runMergeGate(t, commit1, proposalID)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expiration error, got %v", err)
	}

	// Tally should show EXPIRED status
	summary, err := TallyProposal(path, proposalID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "status: EXPIRED") {
		t.Errorf("expected EXPIRED status in tally, got %q", summary)
	}
}


