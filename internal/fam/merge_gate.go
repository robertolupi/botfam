package fam

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/ccrep"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/store"
)

type CcrepEvent struct {
	Type            string
	ProposalID      string
	CommitSHA       string
	Reviewer        string
	ClaimedReviewer string
	Verdict         string
	TS              float64
	SpoofSuspect    bool
	Quorum          string
	Deadline        string
}

func requiredIndependentApprovals(quorumType string, roster []string, author string) int {
	if len(roster) == 0 {
		return 1
	}
	isAuthorInRoster := false
	for _, name := range roster {
		if name == author {
			isAuthorInRoster = true
			break
		}
	}
	totalMembers := len(roster)
	switch strings.ToLower(quorumType) {
	case "all":
		if isAuthorInRoster {
			if totalMembers <= 1 {
				return 0
			}
			return totalMembers - 1
		}
		return totalMembers
	case "majority":
		needed := totalMembers/2 + 1
		if isAuthorInRoster {
			if needed <= 1 {
				return 0
			}
			return needed - 1
		}
		return needed
	default:
		return 1
	}
}

func MergeGateCmd(args []string, out io.Writer) (err error) {
	var commitSHA string
	var proposalID string
	var client *forge.Client
	var independentApprovals []string

	defer func() {
		if UseForge() && client != nil && commitSHA != "" && proposalID != "" {
			// Skip posting status check for CLI/usage errors
			if err != nil && (strings.HasPrefix(err.Error(), "unknown argument") || strings.HasPrefix(err.Error(), "missing required")) {
				return
			}

			state := "success"
			desc := "Consensus MET (approved via botfam-next)"
			if err != nil {
				if strings.Contains(err.Error(), "blocked by request_changes") || strings.Contains(err.Error(), "reject") {
					state = "failure"
				} else {
					state = "pending"
				}
				desc = err.Error()
				if len(desc) > 140 {
					desc = desc[:137] + "..."
				}
			} else if len(independentApprovals) > 0 {
				desc = fmt.Sprintf("Consensus MET with %d independent approval(s)", len(independentApprovals))
			}

			postErr := client.PostCommitStatus(commitSHA, state, "ccrep-merge-gate", desc)
			if postErr != nil {
				fmt.Fprintf(out, "Warning: failed to post commit status check: %v\n", postErr)
			}
		}
	}()

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

	if !UseForge() {
		historyPath := os.Getenv("COLLAB_HISTORY")
		if historyPath == "" {
			historyPath, _ = DefaultHistoryPath(".")
		}
		if historyPath != "" && ValidateHistoryPath(historyPath) == nil {
			events, _, _, err := CollectIrcCcrepEvents(historyPath, proposalID)
			if err == nil {
				for _, ev := range events {
					if ev.SpoofSuspect {
						fmt.Fprintf(out, "spoof-suspect: %s from %q claims reviewer %q (commit %s); event not counted\n", ev.Type, ev.Reviewer, ev.ClaimedReviewer, ev.CommitSHA)
					}
				}
			}
		}
	}

	if UseForge() {
		info, err := (Resolver{WorkDir: "."}).Resolve()
		if err == nil {
			actor := os.Getenv("COLLAB_ACTOR")
			if actor == "" {
				actor = info.Actor
			}
			if actor == "" {
				actor = "operator"
			}
			client, _ = forge.NewClient(".", actor)
		}
	}

	engine, err := buildEngine(context.Background(), proposalID, false)
	if err != nil {
		return err
	}

	res, gateErr := engine.Gate(context.Background(), ccrep.GateArgs{
		ID:     proposalID,
		Commit: commitSHA,
	})
	if gateErr != nil {
		err = gateErr
		return err
	}

	t, tallyErr := engine.Tally(context.Background(), ccrep.TallyArgs{ID: proposalID})
	if tallyErr != nil {
		err = tallyErr
		return err
	}

	if !res.Passed {
		// Reconstruct the legacy errors so existing tests pass:
		if t.LatestSHA != "" && commitSHA != "" && !shaMatches(t.LatestSHA, commitSHA) {
			err = fmt.Errorf("requested commit %s is superseded by newer proposed commit %s; older approvals are void", commitSHA, t.LatestSHA)
			return err
		}

		// Check for insufficient approvals
		var independentApprovalsCount int
		for _, v := range t.Votes {
			if v.Verdict == ccrep.Approve && v.Actor != t.Author && v.Actor != "" && v.SHA == commitSHA {
				independentApprovalsCount++
			}
		}

		// Resolve roster
		resolverInfo, rosterErr := (Resolver{WorkDir: "."}).Resolve()
		var roster []string
		if rosterErr == nil && resolverInfo.Root != "" {
			reg, regErr := ReadRegistry(filepath.Join(resolverInfo.Root, "fam.toml"))
			if regErr == nil {
				roster = reg.Roster
			}
		}

		var present map[string]bool
		historyPath := os.Getenv("COLLAB_HISTORY")
		if historyPath == "" {
			historyPath, _ = DefaultHistoryPath(".")
		}
		if historyPath != "" && ValidateHistoryPath(historyPath) == nil {
			_, p, _, _ := CollectIrcCcrepEvents(historyPath, proposalID)
			present = p
		}

		var presentRoster []string
		for _, name := range roster {
			if present[name] {
				presentRoster = append(presentRoster, name)
			}
		}

		if UseForge() && len(presentRoster) == 0 {
			presentRoster = roster
		}

		requiredApprovals := requiredIndependentApprovals(string(t.Quorum), presentRoster, t.Author)

		if independentApprovalsCount < requiredApprovals {
			err = fmt.Errorf("proposal %s commit %s has %d independent approval(s), but requires %d (quorum: %s, author: %q)",
				proposalID, commitSHA, independentApprovalsCount, requiredApprovals, t.Quorum, t.Author)
			return err
		}

		// Check for blockers
		var blockers []string
		for _, v := range t.Votes {
			if v.Verdict == ccrep.RequestChanges {
				blockers = append(blockers, fmt.Sprintf("%s (%s on commit %s)", v.Actor, "request_changes", v.SHA))
			}
		}
		if len(blockers) > 0 {
			err = fmt.Errorf("proposal %s commit %s is blocked by request_changes/reject from: %s", proposalID, commitSHA, strings.Join(blockers, ", "))
			return err
		}

		// Fallback for other non-passed reasons
		err = fmt.Errorf("consensus gate failed: %s", res.Reason)
		return err
	}

	// Capture independent approvals for the defer block
	for _, v := range t.Votes {
		if v.Verdict == ccrep.Approve && v.Actor != t.Author && v.Actor != "" && v.SHA == commitSHA {
			independentApprovals = append(independentApprovals, v.Actor)
		}
	}

	fmt.Fprintf(out, "Consensus reached: proposal %s approved for commit %s with %d independent approval(s)\n", proposalID, commitSHA, len(independentApprovals))
	err = nil
	return nil
}

func printMergeGateHelp(out io.Writer) error {
	fmt.Fprint(out, "Usage:\n  botfam merge-gate --commit <sha> --proposal <id>\n")
	return nil
}

func newCcrepEvent(typ, proposalID, commitSHA, verdict, authIdentity, claimedReviewer string, ts float64, quorum, deadline string) CcrepEvent {
	authIdentity = strings.TrimSuffix(authIdentity, "-cli")
	claimedReviewer = strings.TrimSuffix(claimedReviewer, "-cli")
	return CcrepEvent{
		Type:            typ,
		ProposalID:      proposalID,
		CommitSHA:       commitSHA,
		Reviewer:        authIdentity,
		ClaimedReviewer: claimedReviewer,
		Verdict:         verdict,
		TS:              ts,
		SpoofSuspect:    claimedReviewer != "" && claimedReviewer != authIdentity,
		Quorum:          quorum,
		Deadline:        deadline,
	}
}

func normalizeReviewer(nick string, roster []string) string {
	// Trim any "-cli" suffix first (since both agy-dc-cli and agy-cli exist)
	nick = strings.TrimSuffix(nick, "-cli")

	// Exact roster match wins first
	for _, name := range roster {
		if nick == name {
			return name
		}
	}

	// Try to match roster names as prefixes followed by a hyphen
	// Match longest roster name first to avoid prefix conflicts (e.g. "alice" vs "alice-extra")
	var sortedRoster []string
	sortedRoster = append(sortedRoster, roster...)
	sort.Slice(sortedRoster, func(i, j int) bool {
		return len(sortedRoster[i]) > len(sortedRoster[j])
	})

	for _, name := range sortedRoster {
		if strings.HasPrefix(nick, name+"-") {
			// Ensure there is a non-empty suffix after the hyphen
			suffix := strings.TrimPrefix(nick, name+"-")
			if len(suffix) > 0 {
				return name
			}
		}
	}

	return nick
}

func parseBangLine(body string) (map[string]string, string) {
	if !strings.HasPrefix(body, "!") {
		return nil, ""
	}
	parts := strings.Fields(body)
	if len(parts) == 0 {
		return nil, ""
	}
	verb := parts[0]
	m := make(map[string]string)

	var currentKey string
	var currentVal []string

	for _, part := range parts[1:] {
		if strings.Contains(part, "=") {
			if currentKey != "" {
				m[currentKey] = strings.Join(currentVal, " ")
			}
			k, v, _ := strings.Cut(part, "=")
			currentKey = k
			currentVal = []string{v}
		} else {
			if currentKey != "" {
				currentVal = append(currentVal, part)
			}
		}
	}
	if currentKey != "" {
		m[currentKey] = strings.Join(currentVal, " ")
	}
	return m, verb
}

// CollectIrcCcrepEvents gathers ccrep:* events for a proposal from the shared
// IRC history log file. It also returns a map of present nicks.
func CollectIrcCcrepEvents(historyPath string, proposalID string) ([]CcrepEvent, map[string]bool, int, error) {
	var events []CcrepEvent
	skipped := 0

	// Resolve roster from fam.toml
	resolverInfo, err := (Resolver{WorkDir: "."}).Resolve()
	var roster []string
	if err == nil && resolverInfo.Root != "" {
		reg, err := ReadRegistry(filepath.Join(resolverInfo.Root, "fam.toml"))
		if err == nil {
			roster = reg.Roster
		}
	}

	file, err := os.Open(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, 0, nil
		}
		return nil, nil, 0, fmt.Errorf("failed to open history file %s: %w", historyPath, err)
	}
	defer file.Close()

	joined := make(map[string]map[string]bool)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry struct {
			Timestamp string `json:"timestamp"`
			Sender    string `json:"sender"`
			Type      string `json:"type"`
			Target    string `json:"target"`
			Body      string `json:"body"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			skipped++
			continue
		}

		// Track presence
		if entry.Type == "JOIN" {
			if joined[entry.Sender] == nil {
				joined[entry.Sender] = make(map[string]bool)
			}
			joined[entry.Sender][entry.Target] = true
		} else if entry.Type == "PART" {
			if joined[entry.Sender] != nil {
				delete(joined[entry.Sender], entry.Target)
			}
		} else if entry.Type == "QUIT" {
			delete(joined, entry.Sender)
		} else if entry.Type == "NICK" {
			if chans, ok := joined[entry.Sender]; ok {
				joined[entry.Target] = chans
				delete(joined, entry.Sender)
			}
		} else if entry.Type == "PRIVMSG" {
			if joined[entry.Sender] == nil {
				joined[entry.Sender] = make(map[string]bool)
			}
			joined[entry.Sender][entry.Target] = true
		}

		if entry.Type != "PRIVMSG" {
			continue
		}

		var bodyMap map[string]any
		var isBang bool
		var bangKeyVals map[string]string
		var bangVerb string

		if err := json.Unmarshal([]byte(entry.Body), &bodyMap); err != nil {
			// Not JSON. Check if it's a bang command
			if strings.HasPrefix(entry.Body, "!") {
				bangKeyVals, bangVerb = parseBangLine(entry.Body)
				if bangVerb != "" {
					isBang = true
				}
			}
			if !isBang {
				continue
			}
		}

		var typ, propID, commitSHA, verdict, claimed, quorum, deadline string

		if isBang {
			switch bangVerb {
			case "!propose":
				typ = "ccrep:proposal"
			case "!evaluate":
				typ = "ccrep:evaluation"
				if bangKeyVals["verdict"] == "request_changes" {
					typ = "ccrep:critique"
				}
			case "!vote":
				typ = "ccrep:evaluation"
			case "!revision":
				typ = "ccrep:revision"
			default:
				continue
			}
			propID = bangKeyVals["id"]
			commitSHA = bangKeyVals["sha"]
			verdict = bangKeyVals["verdict"]
			claimed = bangKeyVals["reviewer"]
			quorum = bangKeyVals["quorum"]
			deadline = bangKeyVals["deadline"]
		} else {
			typ = getString(bodyMap, "type", "Type")
			if !strings.HasPrefix(typ, "ccrep:") {
				continue
			}
			propID = getString(bodyMap, "proposal_id", "proposalId", "proposal-id")
			commitSHA = getString(bodyMap, "commit_sha", "commitSha", "new_commit_sha", "newCommitSha")
			verdict = getString(bodyMap, "verdict", "Verdict")
			claimed = getString(bodyMap, "reviewer", "Reviewer")
			quorum = getString(bodyMap, "quorum", "Quorum")
			deadline = getString(bodyMap, "deadline", "Deadline")
		}

		if propID != proposalID {
			continue
		}

		var ts float64
		t, err := time.Parse(time.RFC3339, entry.Timestamp)
		if err == nil {
			ts = float64(t.UnixNano()) / 1e9
		}

		sender := normalizeReviewer(entry.Sender, roster)
		claimedReviewer := normalizeReviewer(claimed, roster)
		events = append(events, newCcrepEvent(typ, proposalID, commitSHA, verdict, sender, claimedReviewer, ts, quorum, deadline))
	}

	present := make(map[string]bool)
	for nick, chans := range joined {
		if len(chans) > 0 {
			normalized := normalizeReviewer(nick, roster)
			present[normalized] = true
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, 0, fmt.Errorf("error scanning history file: %w", err)
	}

	return events, present, skipped, nil
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

// TallyProposal computes and returns a summarized tally for a given proposal ID.
func TallyProposal(historyPath string, proposalID string) (string, error) {
	events, present, _, err := CollectIrcCcrepEvents(historyPath, proposalID)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return fmt.Sprintf("Proposal %q: no events found in history log", proposalID), nil
	}

	// Sort events by timestamp
	sort.Slice(events, func(i, j int) bool {
		return events[i].TS < events[j].TS
	})

	var author string
	var latestCommitSHA string
	var quorumType string
	var deadlineStr string
	foundProposal := false
	var spoofSuspectsCount int

	var trusted []CcrepEvent
	for _, ev := range events {
		if ev.SpoofSuspect {
			spoofSuspectsCount++
			continue
		}
		trusted = append(trusted, ev)
	}
	events = trusted

	for _, ev := range events {
		if ev.Type == "ccrep:proposal" {
			foundProposal = true
			author = ev.Reviewer
			latestCommitSHA = ev.CommitSHA
			quorumType = ev.Quorum
			deadlineStr = ev.Deadline
		} else if ev.Type == "ccrep:revision" {
			latestCommitSHA = ev.CommitSHA
		}
	}

	if !foundProposal {
		return fmt.Sprintf("Proposal %q: no ccrep:proposal event found (fail closed)", proposalID), nil
	}
	if latestCommitSHA == "" {
		return fmt.Sprintf("Proposal %q: ccrep:proposal has no commit_sha", proposalID), nil
	}

	// Check deadline expiration
	var hasExpired bool
	if deadlineStr != "" {
		deadlineTime, err := time.Parse(time.RFC3339, deadlineStr)
		if err == nil && time.Now().After(deadlineTime) {
			hasExpired = true
		}
	}

	verdicts := make(map[string]CcrepEvent)
	for _, ev := range events {
		if ev.Type == "ccrep:evaluation" || ev.Type == "ccrep:critique" {
			verdicts[ev.Reviewer] = ev
		}
	}

	var approvals []string
	var blockers []string

	for _, ev := range verdicts {
		v := strings.ToLower(ev.Verdict)
		if v == "approve" {
			if ev.CommitSHA == latestCommitSHA {
				approvals = append(approvals, ev.Reviewer)
			}
		} else if v == "request_changes" || v == "reject" {
			blockers = append(blockers, fmt.Sprintf("%s (%s on %s)", ev.Reviewer, ev.Verdict, ev.CommitSHA))
		}
	}

	sort.Strings(approvals)
	sort.Strings(blockers)

	// Check independent approvals
	var independentApprovals []string
	for _, app := range approvals {
		if app != author && app != "" {
			independentApprovals = append(independentApprovals, app)
		}
	}

	// Resolve roster from fam.toml
	info, err := (Resolver{WorkDir: "."}).Resolve()
	var roster []string
	if err == nil && info.Root != "" {
		reg, err := ReadRegistry(filepath.Join(info.Root, "fam.toml"))
		if err == nil {
			roster = reg.Roster
		}
	}

	// Filter roster by presence
	var presentRoster []string
	for _, name := range roster {
		if present[name] {
			presentRoster = append(presentRoster, name)
		}
	}

	requiredApprovals := requiredIndependentApprovals(quorumType, presentRoster, author)

	status := "PENDING"
	if len(blockers) > 0 {
		status = "BLOCKED"
	} else if hasExpired {
		status = "EXPIRED"
	} else if len(independentApprovals) >= requiredApprovals {
		status = "APPROVED"
	}

	var details []string
	details = append(details, fmt.Sprintf("status: %s", status))
	details = append(details, fmt.Sprintf("author: %s", author))
	details = append(details, fmt.Sprintf("commit: %s", latestCommitSHA))
	if len(independentApprovals) > 0 {
		details = append(details, fmt.Sprintf("approvals: %s", strings.Join(independentApprovals, ", ")))
	} else {
		details = append(details, "approvals: none")
	}
	details = append(details, fmt.Sprintf("required_approvals: %d", requiredApprovals))
	if quorumType != "" {
		details = append(details, fmt.Sprintf("quorum: %s", quorumType))
	}
	if deadlineStr != "" {
		details = append(details, fmt.Sprintf("deadline: %s", deadlineStr))
	}
	if len(blockers) > 0 {
		details = append(details, fmt.Sprintf("blockers: %s", strings.Join(blockers, ", ")))
	}
	if spoofSuspectsCount > 0 {
		details = append(details, fmt.Sprintf("ignored_spoof_suspects: %d", spoofSuspectsCount))
	}

	return fmt.Sprintf("Proposal %q tally: %s", proposalID, strings.Join(details, " | ")), nil
}

// CollectCcrepEvents gathers ccrep:* events for a proposal from the session
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
			events = append(events, newCcrepEvent(typ, proposalID, commitSHA, verdict, entry.Actor, claimed, entry.TS, "", ""))
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
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
				if errors.Is(err, os.ErrNotExist) {
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
					if errors.Is(err, os.ErrNotExist) {
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
				events = append(events, newCcrepEvent(msg.Type, proposalID, commitSHA, verdict, msg.From, claimed, msg.TS, "", ""))
			}
		}
	}

	return events, skipped, nil
}
