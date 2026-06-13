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

	"github.com/robertolupi/botfam/internal/ccrep"
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

func buildEngine(ctx context.Context, proposalID string, isMerge bool) (*ccrep.Engine, error) {
	info, err := (Resolver{WorkDir: "."}).Resolve()
	if err != nil {
		return nil, err
	}

	actor := os.Getenv("COLLAB_ACTOR")
	if actor == "" {
		actor = info.Actor
	}
	if actor == "" {
		actor = "operator"
	}

	var roster []string
	if info.Root != "" {
		reg, err := ReadRegistry(filepath.Join(info.Root, "fam.toml"))
		if err == nil {
			roster = reg.Roster
		}
	}

	if UseForge() {
		client, err := forge.NewClient(".", actor)
		if err != nil {
			return nil, err
		}
		var vc ccrep.VersionControl
		if isMerge {
			vc = &GiteaVersionControl{Client: client, ProposalID: proposalID, WorkDir: "."}
		} else {
			vc = &GitVersionControl{WorkDir: "."}
		}
		ledger := &GiteaLedger{Client: client, Roster: roster, WorkDir: "."}
		transport := &GiteaTransport{Client: client}
		return ccrep.New(vc, ledger, transport, actor, ccrep.QuorumMajority), nil
	} else if udsActive() {
		vc := &GitVersionControl{WorkDir: "."}
		ledger := &UdsLedger{WorkDir: "."}
		transport := &UdsTransport{WorkDir: ".", Actor: actor, Ledger: ledger}
		return ccrep.New(vc, ledger, transport, actor, ccrep.QuorumMajority), nil
	} else {
		vc := &GitVersionControl{WorkDir: "."}
		ledger := &IrcLedger{WorkDir: ".", Roster: roster}
		transport := &IrcTransport{WorkDir: ".", Actor: actor}
		return ccrep.New(vc, ledger, transport, actor, ccrep.QuorumMajority), nil
	}
}

func shaMatches(full, want string) bool {
	return full == want || strings.HasPrefix(full, want)
}

// VoteCmd casts a vote
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

	engine, err := buildEngine(context.Background(), proposalID, false)
	if err != nil {
		return err
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

	if UseForge() {
		prNum, err := strconv.Atoi(proposalID)
		if err != nil {
			return fmt.Errorf("invalid Gitea proposal (PR) ID: %w", err)
		}
		tally, err := engine.Tally(context.Background(), ccrep.TallyArgs{ID: proposalID})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Submitting PR review to Gitea for PR %d (commit %s) as %s (%s)...\n", prNum, tally.LatestSHA, actor, verdict)

		vArgs := ccrep.VoteArgs{
			ID:      proposalID,
			Verdict: ccrep.Verdict(strings.ToLower(verdict)),
		}
		_, err = engine.Vote(context.Background(), vArgs)
		if err != nil {
			return err
		}

		fmt.Fprintln(out, "Vote successfully submitted to Gitea!")
		return nil
	} else {
		engineVerdict := strings.ToLower(verdict)
		if engineVerdict == "reject" {
			engineVerdict = "request_changes"
		}

		vArgs := ccrep.VoteArgs{
			ID:      proposalID,
			Verdict: ccrep.Verdict(engineVerdict),
		}
		res, err := engine.Vote(context.Background(), vArgs)
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "Casting vote %q on proposal %q (commit %s) as actor %q...\n", verdict, proposalID, res.SHA, actor)
		fmt.Fprintf(out, "Vote connection released: status %s\n", "released")
		return nil
	}
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

	engine, err := buildEngine(context.Background(), proposalID, false)
	if err != nil {
		return err
	}

	tally, err := engine.Tally(context.Background(), ccrep.TallyArgs{ID: proposalID})
	if err != nil {
		return err
	}

	statusStr := tally.Status
	if statusStr == ccrep.StatusApproved {
		statusStr = "MET"
	} else if statusStr == ccrep.StatusChangesRequested {
		statusStr = "BLOCKED"
	} else if statusStr == ccrep.StatusPending {
		statusStr = "PENDING"
	}

	fmt.Fprintf(out, "Proposal:      %s\n", tally.ProposalID)
	fmt.Fprintf(out, "Author:        %s\n", tally.Author)
	fmt.Fprintf(out, "Latest SHA:    %s\n", tally.LatestSHA)
	fmt.Fprintf(out, "Decision Rule: %s\n", tally.Quorum)
	fmt.Fprintf(out, "Status:        %s\n", statusStr)
	fmt.Fprintln(out, "Votes:")
	if len(tally.Votes) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		for _, v := range tally.Votes {
			verdictStr := string(v.Verdict)
			if verdictStr == "request_changes" {
				verdictStr = "reject"
			}
			if UseForge() {
				fmt.Fprintf(out, "  - %s: %s (commit: %s, present: %t)\n",
					v.Actor, verdictStr, v.SHA, v.Present)
			} else {
				fmt.Fprintf(out, "  - %s: %s (commit: %s, present: %t, provenance: %s)\n",
					v.Actor, verdictStr, v.SHA, v.Present, "ccrep")
			}
		}
	}
	return nil
}

// ProposeCmd generates and appends a ccrep:proposal event
func ProposeCmd(args []string, out io.Writer) error {
	var proposalID string
	var quorum string
	var deadlineStr string
	var summary string
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
		} else if strings.HasPrefix(arg, "--summary=") {
			summary = strings.TrimPrefix(arg, "--summary=")
		} else if arg == "--summary" {
			i++
			if i < len(args) {
				summary = args[i]
			}
		} else {
			return fmt.Errorf("unknown propose argument %q", arg)
		}
	}

	if proposalID == "" {
		return errors.New("missing required --proposal <id>")
	}

	engine, err := buildEngine(context.Background(), proposalID, false)
	if err != nil {
		return err
	}

	if quorum == "" || quorum == "consensus" {
		quorum = "majority"
	}
	if summary == "" {
		summary = "Proposal " + proposalID
	}

	var formattedDeadline string
	if deadlineStr != "" {
		if d, err := time.ParseDuration(deadlineStr); err == nil {
			formattedDeadline = time.Now().Add(d).Format(time.RFC3339)
		} else if ts, err := strconv.ParseInt(deadlineStr, 10, 64); err == nil {
			formattedDeadline = time.Unix(ts, 0).Format(time.RFC3339)
		} else {
			formattedDeadline = deadlineStr
		}
	}

	res, err := engine.Propose(context.Background(), ccrep.ProposeArgs{
		ID:       proposalID,
		Summary:  summary,
		Quorum:   ccrep.Quorum(quorum),
		Deadline: formattedDeadline,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Proposed commit %s for proposal %q with quorum %q\n", res.SHA, proposalID, quorum)
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

	engine, err := buildEngine(context.Background(), proposalID, false)
	if err != nil {
		return err
	}

	tally, err := engine.Tally(context.Background(), ccrep.TallyArgs{ID: proposalID})
	if err != nil {
		return err
	}

	if tally.LatestSHA == "" {
		return fmt.Errorf("no active proposal found for id %q; cannot approve", proposalID)
	}

	res, err := engine.Vote(context.Background(), ccrep.VoteArgs{
		ID:      proposalID,
		Verdict: ccrep.Verdict(strings.ToLower(verdict)),
	})
	if err != nil {
		return err
	}

	if UseForge() {
		fmt.Fprintf(out, "Submitting PR review to Gitea for PR %s (commit %s) as %s (%s)...\n", proposalID, res.SHA, actor, verdict)
		fmt.Fprintln(out, "Vote successfully submitted to Gitea!")
	} else {
		fmt.Fprintf(out, "Casting approval %q on proposal %q (commit %s) as actor %q...\n", verdict, proposalID, res.SHA, actor)
		fmt.Fprintf(out, "Approval connection released: status %s\n", "released")
	}
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

	engine, err := buildEngine(context.Background(), proposalID, true)
	if err != nil {
		return err
	}

	tally, err := engine.Tally(context.Background(), ccrep.TallyArgs{ID: proposalID})
	if err != nil {
		return err
	}

	if UseForge() {
		prNum, err := strconv.Atoi(proposalID)
		if err != nil {
			return fmt.Errorf("invalid Gitea proposal (PR) ID: %w", err)
		}

		var buf bytes.Buffer
		gateArgs := []string{"--proposal", proposalID, "--commit", tally.LatestSHA}
		if err := MergeGateCmd(gateArgs, &buf); err != nil {
			return fmt.Errorf("consensus gate failed for PR %d: %w\nOutput:\n%s", prNum, err, buf.String())
		}
		fmt.Fprint(out, buf.String())

		fmt.Fprintf(out, "Consensus MET. Merging PR %d (commit %s) on Gitea...\n", prNum, tally.LatestSHA)

		res, err := engine.Merge(context.Background(), ccrep.MergeArgs{ID: proposalID})
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "Merged successfully. Resulting remote HEAD SHA: %s\n", res.HeadSHA)
		return nil
	} else {
		if tally.Status != ccrep.StatusApproved {
			statusStr := tally.Status
			if statusStr == ccrep.StatusChangesRequested {
				statusStr = "BLOCKED"
			}
			return fmt.Errorf("cannot merge proposal %q: status is %s (must be MET)", proposalID, statusStr)
		}

		fmt.Fprintf(out, "Consensus MET. Merging commit %s for proposal %q...\n", tally.LatestSHA, proposalID)

		res, err := engine.Merge(context.Background(), ccrep.MergeArgs{ID: proposalID})
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "Merged successfully. Resulting commit SHA: %s\n", res.HeadSHA)
		return nil
	}
}
