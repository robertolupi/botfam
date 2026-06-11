package fam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rlupi/botfam/internal/store"
)

type CcrepEvent struct {
	Type       string
	ProposalID string
	CommitSHA  string
	// Reviewer is the authenticated identity of whoever produced the event:
	// msg.From for mailbox-sourced events, the session entry's actor for
	// session-sourced events. A payload "reviewer" field never overrides it.
	Reviewer string
	// ClaimedReviewer is the payload "reviewer" field, if present. When it
	// differs from Reviewer the event is a spoof suspect and must not count.
	ClaimedReviewer string
	Verdict         string
	TS              float64
	SpoofSuspect    bool
}

func MergeGateCmd(args []string, out io.Writer) error {
	var commitSHA string
	var proposalID string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--commit=") {
			commitSHA = strings.TrimPrefix(arg, "--commit=")
		} else if arg == "--commit" {
			i++
			if i >= len(args) {
				return errors.New("--commit requires a value")
			}
			commitSHA = args[i]
		} else if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i >= len(args) {
				return errors.New("--proposal requires a value")
			}
			proposalID = args[i]
		} else if arg == "-h" || arg == "--help" || arg == "help" {
			return printMergeGateHelp(out)
		} else {
			return fmt.Errorf("unknown argument %q", arg)
		}
	}

	if commitSHA == "" {
		return errors.New("missing required --commit <sha>")
	}
	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}
	st := store.New(info.Root)
	if err := st.Init(); err != nil {
		return err
	}

	events, skipped, err := CollectCcrepEvents(st, proposalID)
	if err != nil {
		return err
	}
	if skipped > 0 {
		fmt.Fprintf(out, "warning: skipped %d unparseable message file(s) while collecting CCREP events\n", skipped)
	}

	if len(events) == 0 {
		return fmt.Errorf("no CCREP events found for proposal %q", proposalID)
	}

	// Sort events by timestamp
	sort.Slice(events, func(i, j int) bool {
		return events[i].TS < events[j].TS
	})

	// Reviewer identity comes from the authenticated channel (msg.From or the
	// session entry's actor). Events whose payload claims a different reviewer
	// are spoof suspects: report them, never count them.
	var spoofSuspects []CcrepEvent
	var trusted []CcrepEvent
	for _, ev := range events {
		if ev.SpoofSuspect {
			spoofSuspects = append(spoofSuspects, ev)
			fmt.Fprintf(out, "spoof-suspect: %s from %q claims reviewer %q (commit %s); event not counted\n", ev.Type, ev.Reviewer, ev.ClaimedReviewer, ev.CommitSHA)
			continue
		}
		trusted = append(trusted, ev)
	}
	events = trusted

	// Find the author and latest proposed commit SHA. Fail closed: without a
	// ccrep:proposal event we cannot establish the author or the latest commit,
	// so the gate refuses rather than guessing.
	var latestCommitSHA string
	var author string
	foundProposal := false
	for _, ev := range events {
		if ev.Type == "ccrep:proposal" {
			foundProposal = true
			author = ev.Reviewer
			latestCommitSHA = ev.CommitSHA
		} else if ev.Type == "ccrep:revision" {
			if ev.CommitSHA == "" {
				return fmt.Errorf("invalid ccrep:revision event for proposal %s (ts %.6f): missing commit_sha", proposalID, ev.TS)
			}
			latestCommitSHA = ev.CommitSHA
		}
	}

	if !foundProposal {
		return fmt.Errorf("no ccrep:proposal event found for proposal %q; refusing to gate (fail closed)", proposalID)
	}
	if latestCommitSHA == "" {
		return fmt.Errorf("ccrep:proposal event for proposal %q has no commit_sha; refusing to gate (fail closed)", proposalID)
	}

	// Approvals die on new commits: if a new commit has been proposed/revisioned,
	// any older approvals are void.
	if latestCommitSHA != commitSHA {
		return fmt.Errorf("requested commit %s is superseded by newer proposed commit %s; older approvals are void", commitSHA, latestCommitSHA)
	}

	// Determine consensus status for this commit
	var approvals []CcrepEvent
	var requests []CcrepEvent

	// Latest-verdict-per-reviewer across the WHOLE proposal: for each reviewer
	// take their most recent verdict (by timestamp) regardless of which commit
	// it targeted. Events are already sorted by TS, so last write wins.
	verdicts := make(map[string]CcrepEvent)
	for _, ev := range events {
		if ev.Type == "ccrep:evaluation" || ev.Type == "ccrep:critique" {
			verdicts[ev.Reviewer] = ev
		}
	}

	for _, ev := range verdicts {
		v := strings.ToLower(ev.Verdict)
		if v == "approve" {
			// An approval only counts for the exact commit being gated
			// (approvals die on new commits). An approval of an older commit
			// is stale: neither an approval nor a blocker.
			if ev.CommitSHA == commitSHA {
				approvals = append(approvals, ev)
			}
		} else if v == "request_changes" || v == "reject" {
			// A blocking verdict persists across revisions: it blocks the gate
			// no matter which commit it targeted, until the same reviewer
			// issues a newer verdict.
			requests = append(requests, ev)
		}
	}

	// Independent approvals (not by author)
	var independentApprovals []CcrepEvent
	for _, app := range approvals {
		if app.Reviewer != author && app.Reviewer != "" {
			independentApprovals = append(independentApprovals, app)
		}
	}
	// Consensus check:
	// NOTE: The current merge gate logic has the following gaps which are acceptable
	// for Wave 1 but should be noted:
	// - It does NOT enforce proposal deadlines/expires_at fields.
	// - It does NOT enforce quorum types (e.g., all vs majority vs any).
	// - The session scan in collectCcrepEvents only sees sessions whose slug
	//   equals the proposal id; CCREP events appended to differently-named
	//   sessions are invisible to the gate.
	// Currently, it accepts any consensus that has >= 1 independent approval and
	// no active blockers (request_changes or reject verdicts). Fuller enforcement
	// is deferred to the Phase 2 ledger.
	//
	// 1. Must have at least one independent approval
	if len(independentApprovals) == 0 {
		suffix := ""
		if len(spoofSuspects) > 0 {
			suffix = fmt.Sprintf("; %d spoof-suspect event(s) ignored", len(spoofSuspects))
		}
		return fmt.Errorf("proposal %s commit %s has no independent approvals (author: %q)%s", proposalID, commitSHA, author, suffix)
	}

	// 2. Must not have any active blocking critiques (request_changes/reject)
	if len(requests) > 0 {
		var blockers []string
		for _, req := range requests {
			blockers = append(blockers, fmt.Sprintf("%s (%s on commit %s)", req.Reviewer, req.Verdict, req.CommitSHA))
		}
		return fmt.Errorf("proposal %s commit %s is blocked by request_changes/reject from: %s", proposalID, commitSHA, strings.Join(blockers, ", "))
	}

	fmt.Fprintf(out, "Consensus reached: proposal %s approved for commit %s with %d independent approval(s)\n", proposalID, commitSHA, len(independentApprovals))
	return nil
}

func printMergeGateHelp(out io.Writer) error {
	fmt.Fprint(out, "Usage:\n  botfam merge-gate --commit <sha> --proposal <id>\n")
	return nil
}

// newCcrepEvent builds an event whose Reviewer is always the authenticated
// identity (msg.From or session entry actor). If the payload claims a
// different reviewer, the event is marked as a spoof suspect.
func newCcrepEvent(typ, proposalID, commitSHA, verdict, authIdentity, claimedReviewer string, ts float64) CcrepEvent {
	return CcrepEvent{
		Type:            typ,
		ProposalID:      proposalID,
		CommitSHA:       commitSHA,
		Reviewer:        authIdentity,
		ClaimedReviewer: claimedReviewer,
		Verdict:         verdict,
		TS:              ts,
		SpoofSuspect:    claimedReviewer != "" && claimedReviewer != authIdentity,
	}
}

// collectCcrepEvents gathers ccrep:* events for a proposal from the session
// log and from every actor mailbox. It returns the events, the number of
// unparseable message files that were skipped, and any I/O error encountered
// (collection errors fail the gate instead of degrading to "no events").
func CollectCcrepEvents(st store.Store, proposalID string) ([]CcrepEvent, int, error) {
	var events []CcrepEvent
	skipped := 0

	// A. Check the session log.
	// NOTE (Wave-1 limitation): this only scans the session whose slug equals
	// the proposal id. CCREP events recorded in sessions with any other slug
	// are not seen by the gate. See also the gap notes in MergeGateCmd.
	sessionDir := filepath.Join(st.RootPath(), "sessions", proposalID)
	if _, statErr := os.Stat(sessionDir); statErr == nil {
		entries, err := st.SessionRead(proposalID, "", 0, 0)
		if err != nil {
			return nil, 0, fmt.Errorf("reading session log for proposal %q: %w", proposalID, err)
		}
		for _, entry := range entries {
			var bodyMap map[string]any
			if err := json.Unmarshal([]byte(entry.Body), &bodyMap); err != nil {
				// Session bodies are free-form (often plain prose); only
				// JSON bodies can carry CCREP events, so skip the rest.
				continue
			}
			typ := getString(bodyMap, "type", "Type")
			if !strings.HasPrefix(typ, "ccrep:") {
				continue
			}
			propID := getString(bodyMap, "proposal_id", "proposalId", "proposal-id")
			if propID != proposalID {
				continue
			}
			commitSHA := getString(bodyMap, "commit_sha", "commitSha", "new_commit_sha", "newCommitSha")
			verdict := getString(bodyMap, "verdict", "Verdict")
			claimed := getString(bodyMap, "reviewer", "Reviewer")
			events = append(events, newCcrepEvent(typ, proposalID, commitSHA, verdict, entry.Actor, claimed, entry.TS))
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, 0, fmt.Errorf("checking session log for proposal %q: %w", proposalID, statErr)
	}

	// B. Check collab messages across all actors
	files, err := os.ReadDir(st.RootPath())
	if err != nil {
		return nil, 0, fmt.Errorf("reading store root %q: %w", st.RootPath(), err)
	}
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		actor := f.Name()
		if actor == "tmp" || actor == "tasks" || actor == "sessions" || strings.HasPrefix(actor, ".") {
			continue
		}
		for _, sub := range []string{"new", "processing", "cur"} {
			subDir := filepath.Join(st.RootPath(), actor, sub)
			msgs, err := os.ReadDir(subDir)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, 0, fmt.Errorf("reading mailbox %q: %w", subDir, err)
			}
			for _, m := range msgs {
				if !strings.HasSuffix(m.Name(), ".json") {
					continue
				}
				msgPath := filepath.Join(subDir, m.Name())
				b, err := os.ReadFile(msgPath)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						// Message moved/acked concurrently; not an error.
						continue
					}
					return nil, 0, fmt.Errorf("reading message %q: %w", msgPath, err)
				}
				var msg store.Message
				if err := json.Unmarshal(b, &msg); err != nil {
					skipped++
					continue
				}
				if !strings.HasPrefix(msg.Type, "ccrep:") {
					continue
				}
				propID := getString(msg.Payload, "proposal_id", "proposalId", "proposal-id")
				if propID != proposalID {
					continue
				}
				commitSHA := getString(msg.Payload, "commit_sha", "commitSha", "new_commit_sha", "newCommitSha")
				verdict := getString(msg.Payload, "verdict", "Verdict")
				claimed := getString(msg.Payload, "reviewer", "Reviewer")
				events = append(events, newCcrepEvent(msg.Type, proposalID, commitSHA, verdict, msg.From, claimed, msg.TS))
			}
		}
	}

	return events, skipped, nil
}

func getString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := m[key]; ok {
			if s, ok := val.(string); ok {
				return s
			}
		}
	}
	return ""
}
