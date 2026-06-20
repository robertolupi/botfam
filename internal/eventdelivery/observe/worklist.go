package observe

import (
	"context"
	"fmt"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
)

// TimedObservation pairs an observation with the instant it occurred, for
// watermark classification of a live push stream.
type TimedObservation struct {
	Observation
	At time.Time
}

// artifactPayload is the compact summary recorded as raw_observations.payload_json.
type artifactPayload struct {
	Number    int64  `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url,omitempty"`
	HeadSHA   string `json:"head_sha,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// Standing composes the standing worklist by querying forge for conditions that
// hold regardless of whether the forge emitted a notification. Every generator
// is query-derivable so it survives restart with watermark = now. The
// observations are NOT filtered by the watermark.
//
// Generators:
//   - open issues assigned to the actor (reply_to_issue)
//   - open PRs assigned to the actor (assigned_open)
//   - open PRs with a review request for the actor (review_pr)
//   - authored open PRs, keyed on head SHA so a force-push surfaces (rebuild_pr)
func (o *Observer) Standing(ctx context.Context) ([]Observation, error) {
	actor, err := o.Actor(ctx)
	if err != nil {
		return nil, err
	}
	repo := o.q.RepoSlug()

	var out []Observation

	issues, err := o.q.ListOpenIssuesAssignedTo(ctx, actor)
	if err != nil {
		return nil, fmt.Errorf("standing: assigned issues: %w", err)
	}
	assignedIssueQuery := fmt.Sprintf("GET /repos/%s/issues?state=open&assignee=%s", repo, actor)
	for _, is := range issues {
		out = append(out, Observation{
			Source:         Source,
			Repo:           repo,
			ArtifactKind:   KindIssue,
			ArtifactNumber: is.Index,
			EventKind:      EventAssignedOpen,
			EventKey:       "standing",
			EventClass:     ClassQueryOnly,
			SourceQuery:    assignedIssueQuery,
			PayloadJSON:    issuePayload(is),
		})
	}

	pulls, err := o.q.ListOpenPulls(ctx)
	if err != nil {
		return nil, fmt.Errorf("standing: open pulls: %w", err)
	}
	openPullsQuery := fmt.Sprintf("GET /repos/%s/pulls?state=open", repo)
	for _, pr := range pulls {
		if userInList(actor, pr.Assignees) || userIs(actor, pr.Assignee) {
			out = append(out, Observation{
				Source:         Source,
				Repo:           repo,
				ArtifactKind:   KindPull,
				ArtifactNumber: pr.Index,
				EventKind:      EventAssignedOpen,
				EventKey:       "standing",
				EventClass:     ClassQueryOnly,
				SourceQuery:    openPullsQuery,
				PayloadJSON:    pullPayload(pr),
			})
		}
		if userInList(actor, pr.RequestedReviewers) {
			out = append(out, Observation{
				Source:         Source,
				Repo:           repo,
				ArtifactKind:   KindPull,
				ArtifactNumber: pr.Index,
				EventKind:      EventReviewRequested,
				EventKey:       "standing",
				EventClass:     ClassQueryOnly,
				SourceQuery:    openPullsQuery + " (filter requested_reviewers)",
				PayloadJSON:    pullPayload(pr),
			})
		}
		if userIs(actor, pr.Poster) {
			head := headSHA(pr)
			if head != "" {
				out = append(out, Observation{
					Source:         Source,
					Repo:           repo,
					ArtifactKind:   KindPull,
					ArtifactNumber: pr.Index,
					EventKind:      EventPush,
					EventKey:       head,
					EventClass:     ClassStableID,
					SourceQuery:    openPullsQuery + " (filter poster, compare head.sha)",
					PayloadJSON:    pullPayload(pr),
				})
			}
		}
	}

	return out, nil
}

func issuePayload(is *forge.Issue) string {
	p := artifactPayload{
		Number:  is.Index,
		Title:   is.Title,
		State:   string(is.State),
		HTMLURL: is.HTMLURL,
	}
	if !is.Updated.IsZero() {
		p.UpdatedAt = is.Updated.UTC().Format(time.RFC3339)
	}
	return mustJSON(p)
}

func pullPayload(pr *forge.PullRequest) string {
	p := artifactPayload{
		Number:  pr.Index,
		Title:   pr.Title,
		State:   string(pr.State),
		HTMLURL: pr.HTMLURL,
		HeadSHA: headSHA(pr),
	}
	if pr.Updated != nil && !pr.Updated.IsZero() {
		p.UpdatedAt = pr.Updated.UTC().Format(time.RFC3339)
	}
	return mustJSON(p)
}

func headSHA(pr *forge.PullRequest) string {
	if pr.Head != nil {
		return pr.Head.Sha
	}
	return ""
}

func userIs(login string, u *forge.User) bool {
	return u != nil && u.UserName == login
}

func userInList(login string, users []*forge.User) bool {
	for _, u := range users {
		if userIs(login, u) {
			return true
		}
	}
	return false
}
