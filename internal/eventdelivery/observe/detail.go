package observe

import (
	"context"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
)

// Translation event kinds (derived from thread detail by the diff). Comments and
// reviews carry immutable forge ids (stable-id); state/label changes are
// synthetic-id (advisory, deduped by value).
const (
	EventComment    = "comment"
	EventReview     = "review"
	EventClosed     = "closed"
	EventMerged     = "merged"
	EventLabelAdded = "label_added"
	EventStatus     = "status_changed"
)

// Work-item kinds the translator/gap-poller emit (the supervisor generators).
const (
	WorkInspectNewComment = "inspect_new_comment"
	WorkInspectNewReview  = "inspect_new_review"
	WorkRefreshScope      = "refresh_scope"
	WorkRebuildPR         = "rebuild_pr"
	WorkCheckFailedRun    = "check_failed_run"
)

// CommentRef / ReviewRef / LabelRef are the parts of a thread the diff keys on.
type CommentRef struct {
	ID        int64
	UpdatedAt time.Time
	Author    string
}

type ReviewRef struct {
	ID          int64
	State       string
	SubmittedAt time.Time
	Reviewer    string
}

type LabelRef struct {
	ID   int64
	Name string
}

// ThreadDetail is the full state of an issue/PR thread at observation time, the
// input to the diff translation.
type ThreadDetail struct {
	Repo      string
	Kind      string // KindIssue | KindPull
	Number    int64
	State     string // "open" | "closed"
	Merged    bool
	HeadSHA   string // pull only
	UpdatedAt time.Time
	Comments  []CommentRef
	Reviews   []ReviewRef
	Labels    []LabelRef
}

// Blocker is a dependency edge: an issue/PR that blocks another. Open blockers
// that are out-of-scope hold a close/merge (the dependency gate).
type Blocker struct {
	Number int64
	State  string
}

// Open reports whether the blocker is still unresolved.
func (b Blocker) Open() bool { return b.State != "closed" }

// DetailQuerier is the forge read surface the translator and gap-poller need on
// top of the worklist Querier. *forge.Client satisfies it via ForgeDetailQuerier.
type DetailQuerier interface {
	RepoSlug() string
	FetchThreadDetail(ctx context.Context, kind string, number int64) (ThreadDetail, error)
	ListBlockers(ctx context.Context, number int64) ([]Blocker, error)
	CombinedStatusState(ctx context.Context, ref string) (string, error)
}

// ForgeDetailQuerier adapts *forge.Client to DetailQuerier, composing thread
// detail from the granular forge read endpoints.
type ForgeDetailQuerier struct {
	C *forge.Client
}

func (f ForgeDetailQuerier) RepoSlug() string { return f.C.RepoSlug() }

func (f ForgeDetailQuerier) FetchThreadDetail(ctx context.Context, kind string, number int64) (ThreadDetail, error) {
	d := ThreadDetail{Repo: f.C.RepoSlug(), Kind: kind, Number: number}

	comments, err := f.C.ListIssueComments(ctx, int(number))
	if err != nil {
		return ThreadDetail{}, err
	}
	for _, c := range comments {
		ref := CommentRef{ID: c.ID, UpdatedAt: c.Updated}
		if c.Poster != nil {
			ref.Author = c.Poster.UserName
		}
		d.Comments = append(d.Comments, ref)
	}

	if kind == KindPull {
		pr, err := f.C.GetPR(ctx, int(number))
		if err != nil {
			return ThreadDetail{}, err
		}
		d.State = string(pr.State)
		d.Merged = pr.HasMerged
		if pr.Head != nil {
			d.HeadSHA = pr.Head.Sha
		}
		if pr.Updated != nil {
			d.UpdatedAt = *pr.Updated
		}
		for _, l := range pr.Labels {
			d.Labels = append(d.Labels, LabelRef{ID: l.ID, Name: l.Name})
		}
		reviews, err := f.C.GetPRReviews(ctx, int(number))
		if err != nil {
			return ThreadDetail{}, err
		}
		for _, r := range reviews {
			ref := ReviewRef{ID: r.ID, State: string(r.State), SubmittedAt: r.Submitted}
			if r.Reviewer != nil {
				ref.Reviewer = r.Reviewer.UserName
			}
			d.Reviews = append(d.Reviews, ref)
		}
		return d, nil
	}

	iss, err := f.C.GetIssue(ctx, int(number))
	if err != nil {
		return ThreadDetail{}, err
	}
	d.State = string(iss.State)
	d.UpdatedAt = iss.Updated
	for _, l := range iss.Labels {
		d.Labels = append(d.Labels, LabelRef{ID: l.ID, Name: l.Name})
	}
	return d, nil
}

func (f ForgeDetailQuerier) ListBlockers(ctx context.Context, number int64) ([]Blocker, error) {
	deps, err := f.C.ListIssueDependencies(ctx, int(number))
	if err != nil {
		return nil, err
	}
	out := make([]Blocker, 0, len(deps))
	for _, d := range deps {
		out = append(out, Blocker{Number: d.Index, State: string(d.State)})
	}
	return out, nil
}

func (f ForgeDetailQuerier) CombinedStatusState(ctx context.Context, ref string) (string, error) {
	return f.C.GetCombinedStatusState(ctx, ref)
}
