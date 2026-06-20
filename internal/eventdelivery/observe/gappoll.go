package observe

import (
	"context"
	"database/sql"
	"fmt"
)

// Gap polling covers the transitions Gitea emits no notification for: a PR head
// SHA change (push/force-push) and commit-status (CI) changes. Each is observed
// as a scalar whose change mints a new event; the first value seen for a thread
// is seeded (not dispatched), so booting against an existing head or a settled
// status does not produce spurious work.

// PollHeadSHA records a PR's current head SHA and emits a rebuild work item when
// it has changed since the last poll (force-push detection). The first SHA seen
// for the PR is seeded only.
func (t *Translator) PollHeadSHA(ctx context.Context, db *sql.DB, runID string, number int64, headSHA string, scopeGen int64) (bool, error) {
	if headSHA == "" {
		return false, nil
	}
	return t.observeScalarChange(ctx, db, runID, KindPull, number, EventPush, headSHA, ClassStableID, WorkRebuildPR,
		fmt.Sprintf("Head SHA changed on pull #%d", number), scopeGen)
}

// PollCommitStatus fetches the combined CI status for a PR's head SHA and emits
// a check work item when the (sha, state) pair has changed. The first status
// seen is seeded only.
func (t *Translator) PollCommitStatus(ctx context.Context, db *sql.DB, runID string, number int64, headSHA string, scopeGen int64) (bool, error) {
	if headSHA == "" {
		return false, nil
	}
	state, err := t.q.CombinedStatusState(ctx, headSHA)
	if err != nil {
		return false, err
	}
	if state == "" {
		return false, nil
	}
	value := headSHA + ":" + state
	return t.observeScalarChange(ctx, db, runID, KindPull, number, EventStatus, value, ClassQueryOnly, WorkCheckFailedRun,
		fmt.Sprintf("CI status %q on pull #%d", state, number), scopeGen)
}

// observeScalarChange records a scalar observation (head SHA, CI status) keyed by
// value, dispatching a work item only when the value changes — never for the
// first value observed for that (thread, event-kind).
func (t *Translator) observeScalarChange(ctx context.Context, db *sql.DB, runID, kind string, number int64, eventKind, value, class, workKind, title string, scopeGen int64) (bool, error) {
	repo := t.q.RepoSlug()
	first, err := isFirstScalar(ctx, db, repo, kind, number, eventKind)
	if err != nil {
		return false, err
	}
	obs := Observation{
		Source:         Source,
		Repo:           repo,
		ArtifactKind:   kind,
		ArtifactNumber: number,
		EventKind:      eventKind,
		EventKey:       value,
		EventClass:     class,
		SourceQuery:    "gap poll",
		PayloadJSON:    "{}",
	}
	inserted, err := recordObservation(ctx, db, runID, obs)
	if err != nil {
		return false, err
	}
	if first || !inserted {
		// First value for this thread is the baseline; an unchanged value is a
		// no-op. Either way, no work item.
		return false, nil
	}
	if err := createWorkItem(ctx, db, scopeGen, obs.EventID(), workKind, title); err != nil {
		return false, err
	}
	return true, nil
}

// isFirstScalar reports whether no value has yet been recorded for this
// (thread, event-kind) — i.e. this poll establishes the baseline.
func isFirstScalar(ctx context.Context, db *sql.DB, repo, kind string, number int64, eventKind string) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM raw_observations WHERE source = ? AND repo = ? AND artifact_kind = ? AND artifact_number = ? AND event_kind = ?`,
		Source, repo, kind, number, eventKind).Scan(&n); err != nil {
		return false, fmt.Errorf("first-scalar check: %w", err)
	}
	return n == 0, nil
}
