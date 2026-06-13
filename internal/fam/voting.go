package fam

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
)

func getSocketPath() (string, error) {
	if path := os.Getenv("BOTFAM_SOCKET"); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".botfam", "daemon.sock")
	if len(path) > 104 {
		h := sha256.Sum256([]byte(home))
		path = filepath.Join("/tmp", fmt.Sprintf("bf-%s.sock", hex.EncodeToString(h[:])))
	}
	return path, nil
}

// sendDaemonRequest makes a POST request to UDS daemon
func sendDaemonRequest(ctx context.Context, endpoint string, reqPayload any, respVal any) error {
	udsPath, err := getSocketPath()
	if err != nil {
		return err
	}

	if endpoint == "vote" {
		conn, err := net.Dial("unix", udsPath)
		if err != nil {
			return fmt.Errorf("error dialing UDS daemon: %w", err)
		}
		defer conn.Close()

		bodyBytes, err := json.Marshal(reqPayload)
		if err != nil {
			return err
		}

		reqStr := fmt.Sprintf("POST /%s HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n%s",
			endpoint, len(bodyBytes), string(bodyBytes))
		_, err = conn.Write([]byte(reqStr))
		if err != nil {
			return fmt.Errorf("failed to write request: %w", err)
		}

		respBuf := make([]byte, 1024)
		n, err := conn.Read(respBuf)
		if err != nil {
			return fmt.Errorf("failed to read response from daemon: %w", err)
		}
		respStr := string(respBuf[:n])

		if !strings.Contains(respStr, "200 OK") {
			// Try to extract error
			parts := strings.SplitN(respStr, "\r\n\r\n", 2)
			if len(parts) == 2 {
				var errResp struct {
					Error string `json:"error"`
				}
				if json.Unmarshal([]byte(parts[1]), &errResp) == nil && errResp.Error != "" {
					return errors.New(errResp.Error)
				}
			}
			return fmt.Errorf("daemon endpoint %q returned error: %s", endpoint, respStr)
		}

		if respVal != nil {
			parts := strings.SplitN(respStr, "\r\n\r\n", 2)
			if len(parts) == 2 {
				if err := json.Unmarshal([]byte(parts[1]), respVal); err != nil {
					return fmt.Errorf("failed to decode response body: %w", err)
				}
			}
		}

		done := make(chan error, 1)
		go func() {
			buf := make([]byte, 1024)
			for {
				_, err := conn.Read(buf)
				if err != nil {
					done <- err
					return
				}
			}
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if err == io.EOF {
				return nil
			}
			return err
		}
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(dialCtx, "unix", udsPath)
			},
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return err
	}

	url := "http://localhost/" + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error calling UDS daemon endpoint %q: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil && errResp.Error != "" {
			return errors.New(errResp.Error)
		}
		return fmt.Errorf("daemon endpoint %q returned status %s", endpoint, resp.Status)
	}

	if respVal != nil {
		if err := json.NewDecoder(resp.Body).Decode(respVal); err != nil {
			return fmt.Errorf("failed to decode daemon response: %w", err)
		}
	}

	return nil
}

// VoteCmd casts a vote by establishing a persistent connection to the daemon
func VoteCmd(args []string, out io.Writer) error {
	var proposalID string
	var verdict string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i < len(args) {
				proposalID = args[i]
			}
		} else if strings.HasPrefix(arg, "--verdict=") {
			verdict = strings.TrimPrefix(arg, "--verdict=")
		} else if arg == "--verdict" {
			i++
			if i < len(args) {
				verdict = args[i]
			}
		} else {
			return fmt.Errorf("unknown vote argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}
	if verdict == "" {
		return errors.New("missing required --verdict <approve|reject|request_changes>")
	}

	if UseForge() {
		prNum, err := strconv.Atoi(proposalID)
		if err != nil {
			return fmt.Errorf("invalid Gitea proposal (PR) ID: %w", err)
		}

		info, err := (Resolver{WorkDir: "."}).Resolve()
		if err != nil {
			return err
		}

		actor := os.Getenv("COLLAB_ACTOR")
		if actor == "" {
			actor = info.Actor
		}
		if actor == "" {
			actor = "operator"
		}

		client, err := forge.NewClient(".", actor)
		if err != nil {
			return err
		}

		pr, err := client.GetPR(prNum)
		if err != nil {
			return fmt.Errorf("failed to fetch PR %d: %w", prNum, err)
		}

		state := "APPROVED"
		if strings.ToLower(verdict) == "reject" || strings.ToLower(verdict) == "request_changes" {
			state = "REQUEST_CHANGES"
		}

		body := fmt.Sprintf("Vote: %s (via botfam-next)", verdict)
		fmt.Fprintf(out, "Submitting PR review to Gitea for PR %d (commit %s) as %s (%s)...\n", prNum, pr.Head.SHA, actor, state)
		err = client.PostPRReview(prNum, pr.Head.SHA, state, body)
		if err != nil {
			return fmt.Errorf("failed to submit Gitea review: %w", err)
		}

		fmt.Fprintln(out, "Vote successfully submitted to Gitea!")
		return nil
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	commitSHA, err := gitOne(".", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to read git HEAD: %w", err)
	}

	payload := map[string]any{
		"work_dir":    info.Root,
		"actor":       actor,
		"proposal_id": proposalID,
		"verdict":     verdict,
		"commit_sha":  commitSHA,
	}

	fmt.Fprintf(out, "Casting vote %q on proposal %q (commit %s) as actor %q...\n", verdict, proposalID, commitSHA, actor)

	var result struct {
		Status string `json:"status"`
	}
	if err := sendDaemonRequest(context.Background(), "vote", payload, &result); err != nil {
		return err
	}

	fmt.Fprintf(out, "Vote connection released: status %s\n", result.Status)
	return nil
}

// TallyCmd prints the status and all votes for a proposal
func TallyCmd(args []string, out io.Writer) error {
	var proposalID string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i < len(args) {
				proposalID = args[i]
			}
		} else {
			return fmt.Errorf("unknown tally argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}

	if UseForge() {
		prNum, err := strconv.Atoi(proposalID)
		if err != nil {
			return fmt.Errorf("invalid Gitea proposal (PR) ID: %w", err)
		}

		info, err := (Resolver{WorkDir: "."}).Resolve()
		if err != nil {
			return err
		}

		actor := os.Getenv("COLLAB_ACTOR")
		if actor == "" {
			actor = info.Actor
		}
		if actor == "" {
			actor = "operator"
		}

		client, err := forge.NewClient(".", actor)
		if err != nil {
			return err
		}

		pr, err := client.GetPR(prNum)
		if err != nil {
			return fmt.Errorf("failed to fetch PR %d: %w", prNum, err)
		}

		reviews, err := client.GetPRReviews(prNum)
		if err != nil {
			return fmt.Errorf("failed to fetch reviews for PR %d: %w", prNum, err)
		}

		// Resolve roster to normalize names
		resolverInfo, err := (Resolver{WorkDir: "."}).Resolve()
		var roster []string
		if err == nil && resolverInfo.Root != "" {
			reg, err := ReadRegistry(filepath.Join(resolverInfo.Root, "fam.toml"))
			if err == nil {
				roster = reg.Roster
			}
		}

		fmt.Fprintf(out, "Proposal:      %s\n", proposalID)
		fmt.Fprintf(out, "Author:        %s\n", normalizeReviewer(pr.User.Login, roster))
		fmt.Fprintf(out, "Latest SHA:    %s\n", pr.Head.SHA)
		fmt.Fprintf(out, "Mergeable:     %t\n", pr.Mergeable)
		fmt.Fprintf(out, "Status:        %s\n", pr.State)
		fmt.Fprintln(out, "Votes:")
		if len(reviews) == 0 {
			fmt.Fprintln(out, "  (none)")
		} else {
			for _, r := range reviews {
				reviewer := normalizeReviewer(r.User.Login, roster)
				fmt.Fprintf(out, "  - %s: %s (stale: %t, submitted: %s)\n",
					reviewer, r.State, r.Stale, r.SubmittedAt)
			}
		}
		return nil
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	payload := map[string]any{
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

	var result struct {
		ProposalID   string              `json:"proposal_id"`
		Status       string              `json:"status"`
		Votes        map[string]voteInfo `json:"votes"`
		DecisionRule string              `json:"decision_rule"`
		LatestSHA    string              `json:"latest_sha"`
		Author       string              `json:"author"`
	}

	if err := sendDaemonRequest(context.Background(), "tally", payload, &result); err != nil {
		return err
	}

	fmt.Fprintf(out, "Proposal:      %s\n", result.ProposalID)
	fmt.Fprintf(out, "Author:        %s\n", result.Author)
	fmt.Fprintf(out, "Latest SHA:    %s\n", result.LatestSHA)
	fmt.Fprintf(out, "Decision Rule: %s\n", result.DecisionRule)
	fmt.Fprintf(out, "Status:        %s\n", result.Status)
	fmt.Fprintln(out, "Votes:")
	if len(result.Votes) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		for _, v := range result.Votes {
			fmt.Fprintf(out, "  - %s: %s (commit: %s, present: %t, provenance: %s)\n",
				v.Actor, v.Verdict, v.CommitSHA, v.IsPresent, v.Provenance)
		}
	}
	return nil
}

// ProposeCmd generates and appends a ccrep:proposal event
func ProposeCmd(args []string, out io.Writer) error {
	var proposalID string
	var quorum string
	var deadlineStr string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i < len(args) {
				proposalID = args[i]
			}
		} else if strings.HasPrefix(arg, "--quorum=") {
			quorum = strings.TrimPrefix(arg, "--quorum=")
		} else if arg == "--quorum" {
			i++
			if i < len(args) {
				quorum = args[i]
			}
		} else if strings.HasPrefix(arg, "--deadline=") {
			deadlineStr = strings.TrimPrefix(arg, "--deadline=")
		} else if arg == "--deadline" {
			i++
			if i < len(args) {
				deadlineStr = args[i]
			}
		} else {
			return fmt.Errorf("unknown propose argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	commitSHA, err := gitOne(".", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to read git HEAD: %w", err)
	}

	var deadline float64
	if deadlineStr != "" {
		if d, err := time.ParseDuration(deadlineStr); err == nil {
			deadline = float64(time.Now().Add(d).Unix())
		} else if ts, err := strconv.ParseFloat(deadlineStr, 64); err == nil {
			deadline = ts
		} else {
			return fmt.Errorf("invalid deadline %q: must be duration or unix timestamp", deadlineStr)
		}
	}

	if quorum == "" {
		quorum = "majority"
	}

	bodyMap := map[string]any{
		"type":        "ccrep:proposal",
		"proposal_id": proposalID,
		"commit_sha":  commitSHA,
		"reviewer":    actor,
		"payload": map[string]any{
			"proposal_id": proposalID,
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
		"actor":    actor,
		"session":  proposalID,
		"body":     string(bodyBytes),
	}

	if err := sendDaemonRequest(context.Background(), "session_append", payload, nil); err != nil {
		return err
	}

	fmt.Fprintf(out, "Proposed commit %s for proposal %q with quorum %q\n", commitSHA, proposalID, quorum)
	return nil
}

// ApproveCmd approves a proposal (shortcut for vote --verdict approve)
func ApproveCmd(args []string, out io.Writer) error {
	var proposalID string
	var verdict string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i < len(args) {
				proposalID = args[i]
			}
		} else if strings.HasPrefix(arg, "--verdict=") {
			verdict = strings.TrimPrefix(arg, "--verdict=")
		} else if arg == "--verdict" {
			i++
			if i < len(args) {
				verdict = args[i]
			}
		} else {
			return fmt.Errorf("unknown approve argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}
	if verdict == "" {
		verdict = "approve"
	}

	if UseForge() {
		voteArgs := []string{"--proposal", proposalID, "--verdict", verdict}
		return VoteCmd(voteArgs, out)
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	// Tally proposal to get latest SHA
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
		ProposalID string              `json:"proposal_id"`
		LatestSHA  string              `json:"latest_sha"`
		Votes      map[string]voteInfo `json:"votes"`
	}

	if err := sendDaemonRequest(context.Background(), "tally", tallyPayload, &tallyResult); err != nil {
		return err
	}

	if tallyResult.LatestSHA == "" {
		return fmt.Errorf("no active proposal found for id %q; cannot approve", proposalID)
	}

	payload := map[string]any{
		"work_dir":    info.Root,
		"actor":       actor,
		"proposal_id": proposalID,
		"verdict":     verdict,
		"commit_sha":  tallyResult.LatestSHA,
	}

	fmt.Fprintf(out, "Casting approval %q on proposal %q (commit %s) as actor %q...\n", verdict, proposalID, tallyResult.LatestSHA, actor)

	var result struct {
		Status string `json:"status"`
	}
	if err := sendDaemonRequest(context.Background(), "vote", payload, &result); err != nil {
		return err
	}

	fmt.Fprintf(out, "Approval connection released: status %s\n", result.Status)
	return nil
}

// MergeCmd checks the proposal tally and performs git merge if MET
func MergeCmd(args []string, out io.Writer) error {
	var proposalID string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--proposal=") {
			proposalID = strings.TrimPrefix(arg, "--proposal=")
		} else if arg == "--proposal" {
			i++
			if i < len(args) {
				proposalID = args[i]
			}
		} else {
			return fmt.Errorf("unknown merge argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}

	if UseForge() {
		prNum, err := strconv.Atoi(proposalID)
		if err != nil {
			return fmt.Errorf("invalid Gitea proposal (PR) ID: %w", err)
		}

		info, err := (Resolver{WorkDir: "."}).Resolve()
		if err != nil {
			return err
		}

		actor := os.Getenv("COLLAB_ACTOR")
		if actor == "" {
			actor = info.Actor
		}
		if actor == "" {
			actor = "operator"
		}

		client, err := forge.NewClient(".", actor)
		if err != nil {
			return err
		}

		pr, err := client.GetPR(prNum)
		if err != nil {
			return fmt.Errorf("failed to fetch PR %d: %w", prNum, err)
		}

		var buf bytes.Buffer
		gateArgs := []string{"--proposal", proposalID, "--commit", pr.Head.SHA}
		if err := MergeGateCmd(gateArgs, &buf); err != nil {
			return fmt.Errorf("consensus gate failed for PR %d: %w\nOutput:\n%s", prNum, err, buf.String())
		}
		fmt.Fprint(out, buf.String())

		fmt.Fprintf(out, "Consensus MET. Merging PR %d (commit %s) on Gitea...\n", prNum, pr.Head.SHA)

		err = client.PostCommitStatus(pr.Head.SHA, "success", "ccrep-merge-gate", "Consensus met (approved via botfam-next)")
		if err != nil {
			fmt.Fprintf(out, "Warning: failed to post ccrep-merge-gate status check: %v\n", err)
		}

		err = client.MergePR(prNum, "merge", fmt.Sprintf("Merge pull request #%d from %s", prNum, pr.Head.Ref))
		if err != nil {
			return fmt.Errorf("Gitea PR merge failed: %w", err)
		}

		fmt.Fprintln(out, "Merged successfully on Gitea!")
		return nil
	}

	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	// 1. Check tally status
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

	if err := sendDaemonRequest(context.Background(), "tally", tallyPayload, &tallyResult); err != nil {
		return err
	}

	if tallyResult.Status != "MET" {
		return fmt.Errorf("cannot merge proposal %q: status is %s (must be MET)", proposalID, tallyResult.Status)
	}

	// 2. Perform git merge
	fmt.Fprintf(out, "Consensus MET. Merging commit %s for proposal %q...\n", tallyResult.LatestSHA, proposalID)

	repoRoot := RepoPath(".")
	mergeOutput, err := gitOutput(repoRoot, "merge", "--no-ff", "-m", fmt.Sprintf("archive: merge proposal %s", proposalID), tallyResult.LatestSHA)
	if err != nil {
		return fmt.Errorf("git merge failed: %w; output:\n%s", err, string(mergeOutput))
	}
	fmt.Fprint(out, string(mergeOutput))

	// Get the resulting HEAD SHA
	newHeadSHA, err := gitOne(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get new HEAD SHA: %w", err)
	}

	// Snapshot presence
	var presence []string
	var absentees []string
	for _, v := range tallyResult.Votes {
		if v.IsPresent {
			presence = append(presence, v.Actor)
		} else {
			absentees = append(absentees, v.Actor)
		}
	}

	// 3. Emit ccrep:executed event
	bodyMap := map[string]any{
		"type":        "ccrep:executed",
		"proposal_id": proposalID,
		"commit_sha":  newHeadSHA,
		"reviewer":    actor,
		"payload": map[string]any{
			"proposal_id": proposalID,
			"presence":    presence,
			"absentees":   absentees,
		},
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return err
	}

	appendPayload := map[string]any{
		"work_dir": info.Root,
		"actor":    actor,
		"session":  proposalID,
		"body":     string(bodyBytes),
	}

	if err := sendDaemonRequest(context.Background(), "session_append", appendPayload, nil); err != nil {
		return err
	}

	fmt.Fprintf(out, "Merged successfully. Resulting commit SHA: %s\n", newHeadSHA)
	return nil
}
