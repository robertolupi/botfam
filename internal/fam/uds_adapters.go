package fam

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/ccrep"
)

// UdsLedger implements ccrep.Ledger by delegating to the UDS daemon.
type UdsLedger struct {
	WorkDir string
}

func (l *UdsLedger) Tally(ctx context.Context, proposalID string) (ccrep.TallyResult, error) {
	info, err := (Resolver{WorkDir: l.WorkDir}).Resolve()
	if err != nil {
		return ccrep.TallyResult{}, err
	}
	tallyPayload := map[string]any{
		"work_dir":    info.Root,
		"proposal_id": proposalID,
	}

	type voteInfo struct {
		Actor      string    `json:"actor"`
		Verdict    string    `json:"verdict"`
		CommitSHA  string    `json:"commit_sha"`
		Timestamp  time.Time `json:"timestamp"`
		IsPresent  bool      `json:"is_present"`
		Provenance string    `json:"provenance"`
	}
	var tallyResult struct {
		ProposalID   string              `json:"proposal_id"`
		Status       string              `json:"status"`
		Votes        map[string]voteInfo `json:"votes"`
		DecisionRule string              `json:"decision_rule"`
		LatestSHA    string              `json:"latest_sha"`
		Author       string              `json:"author"`
	}

	if err := sendDaemonRequest(ctx, "tally", tallyPayload, &tallyResult); err != nil {
		return ccrep.TallyResult{}, err
	}

	if tallyResult.LatestSHA == "" {
		if head, err := gitOne(l.WorkDir, "rev-parse", "HEAD"); err == nil {
			tallyResult.LatestSHA = head
		}
	}

	status := ccrep.StatusPending
	switch tallyResult.Status {
	case "MET", "APPROVED":
		status = ccrep.StatusApproved
	case "BLOCKED":
		status = ccrep.StatusChangesRequested
	}

	var approvals []string
	var votes []ccrep.Vote
	for _, v := range tallyResult.Votes {
		verdict := ccrep.Comment
		switch strings.ToLower(v.Verdict) {
		case "approve":
			verdict = ccrep.Approve
			if v.Actor != tallyResult.Author {
				approvals = append(approvals, v.Actor)
			}
		case "request_changes", "reject":
			verdict = ccrep.RequestChanges
		}
		votes = append(votes, ccrep.Vote{
			Actor:   v.Actor,
			Verdict: verdict,
			SHA:     v.CommitSHA,
			Present: v.IsPresent,
		})
	}

	return ccrep.TallyResult{
		ProposalID: tallyResult.ProposalID,
		LatestSHA:  tallyResult.LatestSHA,
		Status:     status,
		Quorum:     ccrep.Quorum(tallyResult.DecisionRule),
		Author:     tallyResult.Author,
		Approvals:  approvals,
		Votes:      votes,
	}, nil
}

func (l *UdsLedger) ListProposals(ctx context.Context, filter ccrep.ProposalFilter) ([]ccrep.ProposalView, error) {
	return nil, fmt.Errorf("list proposals not implemented for UDS ledger")
}

func (l *UdsLedger) GetProposal(ctx context.Context, proposalID string) (ccrep.ProposalView, error) {
	t, err := l.Tally(ctx, proposalID)
	if err != nil {
		return ccrep.ProposalView{}, err
	}
	return ccrep.ProposalView{
		ID:        t.ProposalID,
		LatestSHA: t.LatestSHA,
		Status:    t.Status,
		Author:    t.Author,
		Quorum:    t.Quorum,
	}, nil
}

func (l *UdsLedger) Subscribe(ctx context.Context) (<-chan ccrep.Event, error) {
	return nil, fmt.Errorf("subscribe not supported on UDS ledger")
}

// UdsTransport implements ccrep.Transport by translating CCREP lines to UDS daemon requests.
type UdsTransport struct {
	WorkDir string
	Actor   string
	Ledger  *UdsLedger
}

func (t *UdsTransport) Send(ctx context.Context, line string) error {
	info, err := (Resolver{WorkDir: t.WorkDir}).Resolve()
	if err != nil {
		return err
	}

	keyVals, verb := parseBangLine(line)
	if verb == "" {
		return nil
	}

	switch verb {
	case "!propose":
		id := keyVals["id"]
		sha := keyVals["sha"]
		quorum := keyVals["quorum"]
		deadlineStr := keyVals["deadline"]
		summary := keyVals["summary"]

		var deadline float64
		if deadlineStr != "" {
			if tm, err := time.Parse(time.RFC3339, deadlineStr); err == nil {
				deadline = float64(tm.Unix())
			} else if ts, err := strconv.ParseFloat(deadlineStr, 64); err == nil {
				deadline = ts
			}
		}

		bodyMap := map[string]any{
			"type":        "ccrep:proposal",
			"proposal_id": id,
			"commit_sha":  sha,
			"reviewer":    t.Actor,
			"summary":     summary,
			"payload": map[string]any{
				"proposal_id": id,
				"quorum":      quorum,
			},
		}
		if deadline > 0 {
			bodyMap["payload"].(map[string]any)["deadline"] = deadline
		}

		bodyBytes, err := json.Marshal(bodyMap)
		if err != nil {
			return err
		}

		payload := map[string]any{
			"work_dir": info.Root,
			"actor":    t.Actor,
			"session":  id,
			"body":     string(bodyBytes),
		}

		return sendDaemonRequest(ctx, "session_append", payload, nil)

	case "!revision":
		id := keyVals["id"]
		sha := keyVals["sha"]

		bodyMap := map[string]any{
			"type":        "ccrep:revision",
			"proposal_id": id,
			"commit_sha":  sha,
		}

		bodyBytes, err := json.Marshal(bodyMap)
		if err != nil {
			return err
		}

		payload := map[string]any{
			"work_dir": info.Root,
			"actor":    t.Actor,
			"session":  id,
			"body":     string(bodyBytes),
		}

		return sendDaemonRequest(ctx, "session_append", payload, nil)

	case "!vote":
		id := keyVals["id"]
		sha := keyVals["sha"]
		verdict := keyVals["verdict"]

		payload := map[string]any{
			"work_dir":    info.Root,
			"actor":       t.Actor,
			"proposal_id": id,
			"verdict":     verdict,
			"commit_sha":  sha,
		}

		var result struct {
			Status string `json:"status"`
		}
		return sendDaemonRequest(ctx, "vote", payload, &result)

	case "!executed":
		id := keyVals["id"]
		sha := keyVals["sha"]

		tally, err := t.Ledger.Tally(ctx, id)
		if err != nil {
			return err
		}

		var presence []string
		var absentees []string
		for _, v := range tally.Votes {
			if v.Present {
				presence = append(presence, v.Actor)
			} else {
				absentees = append(absentees, v.Actor)
			}
		}

		bodyMap := map[string]any{
			"type":        "ccrep:executed",
			"proposal_id": id,
			"commit_sha":  sha,
			"reviewer":    t.Actor,
			"payload": map[string]any{
				"proposal_id": id,
				"presence":    presence,
				"absentees":   absentees,
			},
		}

		bodyBytes, err := json.Marshal(bodyMap)
		if err != nil {
			return err
		}

		payload := map[string]any{
			"work_dir": info.Root,
			"actor":    t.Actor,
			"session":  id,
			"body":     string(bodyBytes),
		}

		return sendDaemonRequest(ctx, "session_append", payload, nil)
	}

	return nil
}

// udsActive checks whether a UDS daemon is active.
func udsActive() bool {
	return os.Getenv("BOTFAM_SOCKET") != ""
}
