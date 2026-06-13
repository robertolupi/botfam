package fam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/ccrep"
)

// IrcLedger implements ccrep.Ledger by parsing the IRC history log.
type IrcLedger struct {
	WorkDir string
	Roster  []string
}

func (l *IrcLedger) Tally(ctx context.Context, proposalID string) (ccrep.TallyResult, error) {
	historyPath := os.Getenv("COLLAB_HISTORY")
	if historyPath == "" {
		var err error
		historyPath, err = DefaultHistoryPath(l.WorkDir)
		if err != nil {
			return ccrep.TallyResult{}, fmt.Errorf("COLLAB_HISTORY is unset and family root could not be resolved: %w", err)
		}
	}

	if err := ValidateHistoryPath(historyPath); err != nil {
		return ccrep.TallyResult{}, err
	}

	events, present, _, err := CollectIrcCcrepEvents(historyPath, proposalID)
	if err != nil {
		return ccrep.TallyResult{}, err
	}

	if len(events) == 0 {
		return ccrep.TallyResult{}, fmt.Errorf("no CCREP events found for proposal %q", proposalID)
	}

	// Sort events by timestamp
	sort.Slice(events, func(i, j int) bool {
		return events[i].TS < events[j].TS
	})

	var trusted []CcrepEvent
	for _, ev := range events {
		if !ev.SpoofSuspect {
			trusted = append(trusted, ev)
		}
	}
	events = trusted

	var latestCommitSHA string
	var author string
	var quorumType string
	var deadlineStr string
	foundProposal := false

	for _, ev := range events {
		if ev.Type == "ccrep:proposal" {
			foundProposal = true
			author = ev.Reviewer
			latestCommitSHA = ev.CommitSHA
			quorumType = ev.Quorum
			deadlineStr = ev.Deadline
		} else if ev.Type == "ccrep:revision" {
			if ev.CommitSHA == "" {
				return ccrep.TallyResult{}, fmt.Errorf("invalid ccrep:revision event for proposal %s (ts %.6f): missing commit_sha", proposalID, ev.TS)
			}
			latestCommitSHA = ev.CommitSHA
		}
	}

	if !foundProposal {
		return ccrep.TallyResult{}, fmt.Errorf("no ccrep:proposal event found for proposal %q", proposalID)
	}

	// Check deadline
	if deadlineStr != "" {
		deadlineTime, err := time.Parse(time.RFC3339, deadlineStr)
		if err == nil && time.Now().After(deadlineTime) {
			return ccrep.TallyResult{}, fmt.Errorf("proposal %s has expired (deadline: %s)", proposalID, deadlineStr)
		}
	}

	// Latest-verdict-per-reviewer
	verdicts := make(map[string]CcrepEvent)
	for _, ev := range events {
		if ev.Type == "ccrep:evaluation" || ev.Type == "ccrep:critique" {
			verdicts[ev.Reviewer] = ev
		}
	}

	var votes []ccrep.Vote
	var approvals []string
	var hasBlocker bool

	for reviewer, ev := range verdicts {
		v := strings.ToLower(ev.Verdict)
		var verdict ccrep.Verdict
		if v == "approve" {
			verdict = ccrep.Approve
			if reviewer != author {
				approvals = append(approvals, reviewer)
			}
		} else if v == "request_changes" || v == "reject" {
			verdict = ccrep.RequestChanges
			hasBlocker = true
		} else {
			verdict = ccrep.Comment
		}

		votes = append(votes, ccrep.Vote{
			Actor:   reviewer,
			Verdict: verdict,
			SHA:     ev.CommitSHA,
			Present: present[reviewer],
		})
	}

	// Filter roster by presence
	var presentRoster []string
	for _, name := range l.Roster {
		if present[name] {
			presentRoster = append(presentRoster, name)
		}
	}

	// Calculate status
	q := ccrep.Quorum(quorumType)
	if q == "" {
		q = ccrep.QuorumMajority
	}
	requiredApprovals := requiredIndependentApprovals(string(q), presentRoster, author)

	status := ccrep.StatusPending
	if hasBlocker {
		status = ccrep.StatusChangesRequested
	} else if len(approvals) >= requiredApprovals {
		status = ccrep.StatusApproved
	}

	return ccrep.TallyResult{
		ProposalID: proposalID,
		LatestSHA:  latestCommitSHA,
		Status:     status,
		Quorum:     q,
		Author:     author,
		Approvals:  approvals,
		Votes:      votes,
	}, nil
}

func (l *IrcLedger) ListProposals(ctx context.Context, filter ccrep.ProposalFilter) ([]ccrep.ProposalView, error) {
	return nil, fmt.Errorf("list proposals not implemented for IRC ledger")
}

func (l *IrcLedger) GetProposal(ctx context.Context, proposalID string) (ccrep.ProposalView, error) {
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

func (l *IrcLedger) Subscribe(ctx context.Context) (<-chan ccrep.Event, error) {
	return nil, fmt.Errorf("subscribe not supported on IRC ledger")
}

// IrcTransport implements ccrep.Transport by writing to the local IRC client's input pipe.
type IrcTransport struct {
	WorkDir string
	Actor   string
}

func (t *IrcTransport) Send(ctx context.Context, line string) error {
	fifoPath := filepath.Join(t.WorkDir, "scratch", "irc", t.Actor, "in")

	if _, err := os.Stat(fifoPath); os.IsNotExist(err) {
		resolverInfo, err := (Resolver{WorkDir: t.WorkDir}).Resolve()
		if err == nil && resolverInfo.Root != "" {
			fifoPath = filepath.Join(resolverInfo.Root, "scratch", "irc", t.Actor, "in")
		}
	}

	f, err := os.OpenFile(fifoPath, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open IRC client FIFO %s: %w", fifoPath, err)
	}
	defer f.Close()

	ccrepChan := "#ccrep"
	reg, err := ReadRegistry(filepath.Join(t.WorkDir, "fam.toml"))
	if err != nil {
		resolverInfo, rErr := (Resolver{WorkDir: t.WorkDir}).Resolve()
		if rErr == nil && resolverInfo.Root != "" {
			reg, err = ReadRegistry(filepath.Join(resolverInfo.Root, "fam.toml"))
		}
	}
	if err == nil {
		_, ccrep := FamChannels(reg)
		if ccrep != "" {
			ccrepChan = ccrep
		}
	}

	formattedLine := fmt.Sprintf("/msg %s %s\n", ccrepChan, line)
	if _, err := f.WriteString(formattedLine); err != nil {
		return fmt.Errorf("failed to write to IRC FIFO: %w", err)
	}

	return nil
}
