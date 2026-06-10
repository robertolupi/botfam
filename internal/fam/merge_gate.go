package fam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Reviewer   string
	Verdict    string
	TS         float64
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

	events, err := collectCcrepEvents(st, proposalID)
	if err != nil {
		return err
	}

	if len(events) == 0 {
		return fmt.Errorf("no CCREP events found for proposal %q", proposalID)
	}

	// Sort events by timestamp
	sort.Slice(events, func(i, j int) bool {
		return events[i].TS < events[j].TS
	})

	// Find the author and latest proposed commit SHA
	var latestCommitSHA string
	var author string
	for _, ev := range events {
		if ev.Type == "ccrep:proposal" {
			latestCommitSHA = ev.CommitSHA
			author = ev.Reviewer
		} else if ev.Type == "ccrep:revision" {
			latestCommitSHA = ev.CommitSHA
		}
	}

	if latestCommitSHA == "" {
		// If we couldn't find any explicit proposal/revision, fallback to checking if the specified commit matches
		latestCommitSHA = commitSHA
	}

	// Approvals die on new commits: if a new commit has been proposed/revisioned,
	// any older approvals are void.
	if latestCommitSHA != commitSHA {
		return fmt.Errorf("requested commit %s is superseded by newer proposed commit %s; older approvals are void", commitSHA, latestCommitSHA)
	}

	// Determine consensus status for this commit
	var approvals []CcrepEvent
	var requests []CcrepEvent

	// We want to find the latest verdict by each reviewer for this exact commit SHA
	verdicts := make(map[string]CcrepEvent)
	for _, ev := range events {
		if ev.CommitSHA == commitSHA && (ev.Type == "ccrep:evaluation" || ev.Type == "ccrep:critique") {
			verdicts[ev.Reviewer] = ev
		}
	}

	for _, ev := range verdicts {
		v := strings.ToLower(ev.Verdict)
		if v == "approve" {
			approvals = append(approvals, ev)
		} else if v == "request_changes" || v == "reject" {
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
	// Currently, it accepts any consensus that has >= 1 independent approval and
	// no active blockers (request_changes or reject verdicts). Fuller enforcement
	// is deferred to the Phase 2 ledger.
	//
	// 1. Must have at least one independent approval
	if len(independentApprovals) == 0 {
		return fmt.Errorf("proposal %s commit %s has no independent approvals (author: %q)", proposalID, commitSHA, author)
	}

	// 2. Must not have any active blocking critiques (request_changes/reject)
	if len(requests) > 0 {
		var blockers []string
		for _, req := range requests {
			blockers = append(blockers, fmt.Sprintf("%s (%s)", req.Reviewer, req.Verdict))
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

func collectCcrepEvents(st *store.Store, proposalID string) ([]CcrepEvent, error) {
	var events []CcrepEvent

	// A. Check the session log
	entries, err := st.SessionRead(proposalID, "", 0, 0)
	if err == nil {
		for _, entry := range entries {
			var bodyMap map[string]any
			if err := json.Unmarshal([]byte(entry.Body), &bodyMap); err == nil {
				typ := getString(bodyMap, "type", "Type")
				if strings.HasPrefix(typ, "ccrep:") {
					propID := getString(bodyMap, "proposal_id", "proposalId", "proposal-id")
					if propID == proposalID {
						commitSHA := getString(bodyMap, "commit_sha", "commitSha", "new_commit_sha", "newCommitSha")
						verdict := getString(bodyMap, "verdict", "Verdict")
						reviewer := getString(bodyMap, "reviewer", "Reviewer")
						if reviewer == "" {
							reviewer = entry.Actor
						}
						events = append(events, CcrepEvent{
							Type:       typ,
							ProposalID: proposalID,
							CommitSHA:  commitSHA,
							Reviewer:   reviewer,
							Verdict:    verdict,
							TS:         entry.TS,
						})
					}
				}
			}
		}
	}

	// B. Check collab messages across all actors
	files, err := os.ReadDir(st.Root)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() {
				continue
			}
			actor := f.Name()
			if actor == "tmp" || actor == "tasks" || actor == "sessions" || strings.HasPrefix(actor, ".") {
				continue
			}
			for _, sub := range []string{"new", "processing", "cur"} {
				subDir := filepath.Join(st.Root, actor, sub)
				msgs, err := os.ReadDir(subDir)
				if err != nil {
					continue
				}
				for _, m := range msgs {
					if !strings.HasSuffix(m.Name(), ".json") {
						continue
					}
					msgPath := filepath.Join(subDir, m.Name())
					b, err := os.ReadFile(msgPath)
					if err != nil {
						continue
					}
					var msg store.Message
					if err := json.Unmarshal(b, &msg); err != nil {
						continue
					}
					if strings.HasPrefix(msg.Type, "ccrep:") {
						propID := getString(msg.Payload, "proposal_id", "proposalId", "proposal-id")
						if propID == proposalID {
							commitSHA := getString(msg.Payload, "commit_sha", "commitSha", "new_commit_sha", "newCommitSha")
							verdict := getString(msg.Payload, "verdict", "Verdict")
							reviewer := getString(msg.Payload, "reviewer", "Reviewer")
							if reviewer == "" {
								reviewer = msg.From
							}
							events = append(events, CcrepEvent{
								Type:       msg.Type,
								ProposalID: proposalID,
								CommitSHA:  commitSHA,
								Reviewer:   reviewer,
								Verdict:    verdict,
								TS:         msg.TS,
							})
						}
					}
				}
			}
		}
	}

	return events, nil
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
