package observe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/robertolupi/botfam/internal/forge"
)

// Querier is the read-only forge surface the observer depends on. *forge.Client
// satisfies it; tests supply a fake.
type Querier interface {
	// AuthLogin returns the login of the token owner — the actor whose standing
	// work the observer composes.
	AuthLogin(ctx context.Context) (string, error)
	// RepoSlug is the "owner/repo" identity used in event identities.
	RepoSlug() string
	// ListOpenIssuesAssignedTo returns open issues assigned to actor.
	ListOpenIssuesAssignedTo(ctx context.Context, actor string) ([]*forge.Issue, error)
	// ListOpenPulls returns all open pull requests (filtered in memory).
	ListOpenPulls(ctx context.Context) ([]*forge.PullRequest, error)
	// ListAllUnreadNotifications returns ALL of the actor's unread notification
	// threads (paginated), not just the first page — the bootstrap must not drop
	// pokes past a page boundary.
	ListAllUnreadNotifications(ctx context.Context) ([]forge.Notification, error)
}

// Observer composes the standing worklist and the unread bootstrap for one
// session. It holds the session watermark, but Standing and Bootstrap are never
// gated by it — see SessionWatermark.
type Observer struct {
	q  Querier
	wm SessionWatermark

	actor string // cached after first resolution
}

// New returns an Observer for the given forge querier and session watermark.
func New(q Querier, wm SessionWatermark) *Observer {
	return &Observer{q: q, wm: wm}
}

// Watermark returns the session watermark.
func (o *Observer) Watermark() SessionWatermark { return o.wm }

// Actor resolves (and caches) the actor login from the forge token.
func (o *Observer) Actor(ctx context.Context) (string, error) {
	if o.actor != "" {
		return o.actor, nil
	}
	actor, err := o.q.AuthLogin(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve actor: %w", err)
	}
	if actor == "" {
		return "", fmt.Errorf("forge returned empty actor login")
	}
	o.actor = actor
	return actor, nil
}

// Poll returns the full observation set for one supervisor poll: the standing
// worklist plus the pre-session unread bootstrap, deduplicated by EventID. It is
// the input to ingestion. Neither source is gated by the watermark, so standing
// work and pre-session pokes both survive restarts with watermark = now.
func (o *Observer) Poll(ctx context.Context) ([]Observation, error) {
	standing, err := o.Standing(ctx)
	if err != nil {
		return nil, err
	}
	bootstrap, err := o.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	return dedupe(append(standing, bootstrap...)), nil
}

// LiveWakes filters observations down to those that occurred strictly after the
// session start, applying the watermark. This is for the (future) live push
// stream only; it must NOT be applied to Standing or Bootstrap results. Each
// observation is paired with the time it occurred.
func (o *Observer) LiveWakes(obs []TimedObservation) []Observation {
	out := make([]Observation, 0, len(obs))
	for _, t := range obs {
		if o.wm.IsLiveWake(t.At) {
			out = append(out, t.Observation)
		}
	}
	return out
}

func dedupe(obs []Observation) []Observation {
	seen := make(map[string]bool, len(obs))
	out := make([]Observation, 0, len(obs))
	for _, ob := range obs {
		id := ob.EventID()
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, ob)
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
