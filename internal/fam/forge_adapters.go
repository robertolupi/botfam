package fam

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/robertolupi/botfam/internal/ccrep"
	"github.com/robertolupi/botfam/internal/forge"
)

// GiteaLedger implements ccrep.Ledger
type GiteaLedger struct {
	Client  *forge.Client
	Roster  []string
	WorkDir string
}

func (l *GiteaLedger) Tally(ctx context.Context, proposalID string) (ccrep.TallyResult, error) {
	prNum, err := strconv.Atoi(proposalID)
	if err != nil {
		return ccrep.TallyResult{}, fmt.Errorf("invalid Gitea proposal (PR) ID %q: %w", proposalID, err)
	}

	pr, err := l.Client.GetPR(prNum)
	if err != nil {
		return ccrep.TallyResult{}, fmt.Errorf("failed to fetch PR %d: %w", prNum, err)
	}

	reviews, err := l.Client.GetPRReviews(prNum)
	if err != nil {
		return ccrep.TallyResult{}, fmt.Errorf("failed to fetch reviews for PR %d: %w", prNum, err)
	}

	author := normalizeReviewer(pr.User.Login, l.Roster)

	// Determine latest verdicts per reviewer (last write wins)
	verdicts := make(map[string]*forge.Review)
	for _, r := range reviews {
		if r.Stale {
			continue
		}
		verdicts[normalizeReviewer(r.User.Login, l.Roster)] = r
	}

	// Load presence from IRC history file (if available)
	present := make(map[string]bool)
	historyPath := os.Getenv("COLLAB_HISTORY")
	if historyPath == "" {
		historyPath, _ = DefaultHistoryPath(l.WorkDir)
	}
	if historyPath != "" && ValidateHistoryPath(historyPath) == nil {
		if _, p, _, err := CollectIrcCcrepEvents(historyPath, proposalID); err == nil {
			present = p
		}
	}

	var votes []ccrep.Vote
	var approvals []string
	var hasBlocker bool

	for reviewer, r := range verdicts {
		var verdict ccrep.Verdict
		if r.State == "APPROVED" {
			verdict = ccrep.Approve
			// Only count approvals from non-authors
			if reviewer != author {
				votes = append(votes, ccrep.Vote{
					Actor:   reviewer,
					Verdict: verdict,
					SHA:     pr.Head.SHA,
					Present: present[reviewer] || len(present) == 0,
				})
				approvals = append(approvals, reviewer)
			}
		} else if r.State == "REQUEST_CHANGES" {
			verdict = ccrep.RequestChanges
			hasBlocker = true
			votes = append(votes, ccrep.Vote{
				Actor:   reviewer,
				Verdict: verdict,
				SHA:     pr.Head.SHA,
				Present: present[reviewer] || len(present) == 0,
			})
		}
	}

	// Filter roster by presence
	var presentRoster []string
	for _, name := range l.Roster {
		if present[name] {
			presentRoster = append(presentRoster, name)
		}
	}

	// Fallback/Warning: if no presence info is found (e.g. IRC log is missing or empty),
	// we fall back to all roster members present but log a warning.
	if len(presentRoster) == 0 {
		presentRoster = l.Roster
		fmt.Fprintf(os.Stderr, "warning: no active IRC presence found for proposal %s; falling back to full roster\n", proposalID)
	}

	// Calculate required approvals
	requiredApprovals := requiredIndependentApprovals("majority", presentRoster, author)

	status := ccrep.StatusPending
	if hasBlocker {
		status = ccrep.StatusChangesRequested
	} else if len(approvals) >= requiredApprovals {
		status = ccrep.StatusApproved
	}

	return ccrep.TallyResult{
		ProposalID: proposalID,
		LatestSHA:  pr.Head.SHA,
		Status:     status,
		Quorum:     ccrep.QuorumMajority,
		Author:     author,
		Approvals:  approvals,
		Votes:      votes,
	}, nil
}

func (l *GiteaLedger) ListProposals(ctx context.Context, filter ccrep.ProposalFilter) ([]ccrep.ProposalView, error) {
	return nil, fmt.Errorf("list proposals not implemented for Gitea ledger")
}

func (l *GiteaLedger) GetProposal(ctx context.Context, proposalID string) (ccrep.ProposalView, error) {
	prNum, err := strconv.Atoi(proposalID)
	if err != nil {
		return ccrep.ProposalView{}, err
	}
	pr, err := l.Client.GetPR(prNum)
	if err != nil {
		return ccrep.ProposalView{}, err
	}
	return ccrep.ProposalView{
		ID:        proposalID,
		LatestSHA: pr.Head.SHA,
		Status:    pr.State,
		Author:    normalizeReviewer(pr.User.Login, l.Roster),
		Quorum:    ccrep.QuorumMajority,
		Summary:   pr.Title,
	}, nil
}

func (l *GiteaLedger) Subscribe(ctx context.Context) (<-chan ccrep.Event, error) {
	return nil, fmt.Errorf("subscribe not supported on Gitea ledger")
}

// GiteaTransport implements ccrep.Transport
type GiteaTransport struct {
	Client *forge.Client
}

func (t *GiteaTransport) Send(ctx context.Context, line string) error {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "!") {
		return nil
	}

	parts := strings.Split(line, " ")
	verb := parts[0]
	keyVals := make(map[string]string)
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		if ok {
			keyVals[k] = strings.Trim(v, `"`)
		}
	}

	proposalID := keyVals["id"]
	prNum, err := strconv.Atoi(proposalID)
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", proposalID, err)
	}

	switch verb {
	case "!propose":
		return nil
	case "!revision":
		return nil
	case "!vote":
		verdict := keyVals["verdict"]
		sha := keyVals["sha"]
		state := "APPROVED"
		if verdict == "request_changes" || verdict == "reject" {
			state = "REQUEST_CHANGES"
		} else if verdict == "comment" {
			state = "COMMENT"
		}
		body := fmt.Sprintf("Vote: %s (via ccrep engine)", verdict)
		return t.Client.PostPRReview(prNum, sha, state, body)
	case "!executed":
		return nil
	}
	return nil
}

// GitVersionControl implements ccrep.VersionControl
type GitVersionControl struct {
	WorkDir string
}

func (g *GitVersionControl) RevParse(ctx context.Context, ref string) (string, error) {
	return gitOne(g.WorkDir, "rev-parse", ref)
}

func (g *GitVersionControl) IsPushed(ctx context.Context, sha string) (bool, error) {
	return true, nil
}

func (g *GitVersionControl) MainCheckout(ctx context.Context) (string, error) {
	resolverInfo, err := (Resolver{WorkDir: g.WorkDir}).Resolve()
	if err != nil {
		return "", err
	}
	reg, err := ReadRegistry(filepath.Join(resolverInfo.Root, "fam.toml"))
	if err != nil {
		return "", err
	}
	if len(reg.RepoPaths) > 0 {
		return reg.RepoPaths[0], nil
	}
	return g.WorkDir, nil
}

func (g *GitVersionControl) MergeNoFF(ctx context.Context, dir, sha, message string) (string, error) {
	_, err := gitOutput(dir, "-c", "user.name=agy", "-c", "user.email=roberto.lupi+agy@gmail.com", "merge", "--no-ff", "-m", message, sha)
	if err != nil {
		return "", err
	}
	return gitOne(dir, "rev-parse", "HEAD")
}

// GiteaVersionControl implements ccrep.VersionControl by executing merges via Gitea API.
type GiteaVersionControl struct {
	Client     *forge.Client
	ProposalID string
	WorkDir    string
}

func (g *GiteaVersionControl) RevParse(ctx context.Context, ref string) (string, error) {
	return gitOne(g.WorkDir, "rev-parse", ref)
}

func (g *GiteaVersionControl) IsPushed(ctx context.Context, sha string) (bool, error) {
	return true, nil
}

func (g *GiteaVersionControl) MainCheckout(ctx context.Context) (string, error) {
	return g.WorkDir, nil
}

func (g *GiteaVersionControl) MergeNoFF(ctx context.Context, dir, sha, message string) (string, error) {
	prNum, err := strconv.Atoi(g.ProposalID)
	if err != nil {
		return "", err
	}

	pr, err := g.Client.GetPR(prNum)
	if err != nil {
		return "", fmt.Errorf("failed to get PR %d: %w", prNum, err)
	}

	// Satisfy the merge-gate status check on Gitea
	_ = g.Client.PostCommitStatus(sha, "success", "ccrep-merge-gate", "Consensus met (approved via botfam-next)")

	err = g.Client.MergePR(prNum, "merge", message)
	if err != nil {
		return "", fmt.Errorf("Gitea PR merge failed: %w", err)
	}

	// Resolve the new HEAD of the target base branch on the Gitea remote
	ref := "refs/heads/" + pr.Base.Ref
	lsOut, err := gitOne(g.WorkDir, "ls-remote", "gitea", ref)
	if err != nil {
		return "", fmt.Errorf("failed to resolve Gitea remote branch HEAD: %w", err)
	}
	parts := strings.Fields(lsOut)
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid ls-remote output: %q", lsOut)
	}
	return parts[0], nil
}
