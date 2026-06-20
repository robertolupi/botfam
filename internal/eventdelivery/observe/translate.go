package observe

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// Translator turns thread-level notifications into event-level work items by
// diffing the thread's current detail against what is already recorded in
// session.db. It is supervisor-internal; nothing here is agent-facing.
type Translator struct {
	q  DetailQuerier
	wm SessionWatermark
}

// NewTranslator returns a Translator for the given forge detail surface and the
// session watermark (used as the default first-observation baseline).
func NewTranslator(q DetailQuerier, wm SessionWatermark) *Translator {
	return &Translator{q: q, wm: wm}
}

// ThreadRef identifies a thread to translate. BaselineAt overrides the default
// baseline (the session watermark) — e.g. a thread's last_read_at when known.
type ThreadRef struct {
	Kind       string // KindIssue | KindPull
	Number     int64
	BaselineAt time.Time
}

// TranslateResult summarises one translation pass.
type TranslateResult struct {
	Emitted    []EmittedWorkItem // newly created work items
	SeededSeen int               // events recorded as seen but not dispatched (baseline)
	Suppressed int               // events already recorded (deduped across restart)
	FirstSeen  bool              // this was the thread's first translation
}

// EmittedWorkItem is one work item created by translation.
type EmittedWorkItem struct {
	ID       string
	Kind     string
	SourceID string
	EventID  string
}

// translationKinds are the event kinds that count as "this thread has been
// translated before" for first-observation detection (push/status/notification
// are gap/bootstrap kinds and do not).
var translationKinds = []string{EventComment, EventReview, EventClosed, EventMerged, EventLabelAdded}

type derivedEvent struct {
	obs           Observation
	at            time.Time
	firstEligible bool // may be dispatched on first contact if at > baseline
	workKind      string
	title         string
}

// Translate fetches the thread's detail, diffs it against session.db, records
// newly-seen events, and creates work items for the genuinely-new ones.
//
// First-observation baseline: on the thread's first translation, every current
// event is recorded as already-seen, and only timestamped events (comments,
// reviews) newer than the baseline are dispatched — a long pre-existing thread
// does not flood the dispatcher with its history. After first contact any newly
// recorded event is dispatched. Re-translating a thread re-records nothing
// (INSERT OR IGNORE) so restart neither re-dispatches translated events nor
// hides sibling events on the same thread.
func (t *Translator) Translate(ctx context.Context, db *sql.DB, runID string, scopeGen int64, ref ThreadRef) (TranslateResult, error) {
	var res TranslateResult
	detail, err := t.q.FetchThreadDetail(ctx, ref.Kind, ref.Number)
	if err != nil {
		return res, fmt.Errorf("fetch thread detail %s#%d: %w", ref.Kind, ref.Number, err)
	}
	baseline := ref.BaselineAt
	if baseline.IsZero() {
		baseline = t.wm.Start()
	}
	first, err := isFirstContact(ctx, db, detail.Repo, ref.Kind, ref.Number)
	if err != nil {
		return res, err
	}
	res.FirstSeen = first

	for _, ev := range deriveEvents(detail) {
		inserted, err := recordObservation(ctx, db, runID, ev.obs)
		if err != nil {
			return res, err
		}
		if !inserted {
			res.Suppressed++
			continue
		}
		if !decideEmit(first, ev, baseline) {
			res.SeededSeen++
			continue
		}
		eventID := ev.obs.EventID()
		if err := createWorkItem(ctx, db, scopeGen, eventID, ev.workKind, ev.title); err != nil {
			return res, err
		}
		res.Emitted = append(res.Emitted, EmittedWorkItem{
			ID:       workItemID(eventID),
			Kind:     ev.workKind,
			SourceID: eventID,
			EventID:  eventID,
		})
	}
	return res, nil
}

func decideEmit(first bool, ev derivedEvent, baseline time.Time) bool {
	if !first {
		return true
	}
	return ev.firstEligible && ev.at.After(baseline)
}

// deriveEvents expands a thread's detail into candidate events. Comments and
// reviews are stable-id and dispatchable on first contact (timestamp-gated);
// state and label changes are synthetic-id, diffed by value, and never
// dispatched on first contact (we cannot tell pre-existing from new).
func deriveEvents(d ThreadDetail) []derivedEvent {
	var out []derivedEvent
	for _, c := range d.Comments {
		out = append(out, derivedEvent{
			obs:           newObservation(d, EventComment, strconv.FormatInt(c.ID, 10), ClassStableID),
			at:            c.UpdatedAt,
			firstEligible: true,
			workKind:      WorkInspectNewComment,
			title:         fmt.Sprintf("New comment on %s #%d", d.Kind, d.Number),
		})
	}
	for _, r := range d.Reviews {
		out = append(out, derivedEvent{
			obs:           newObservation(d, EventReview, strconv.FormatInt(r.ID, 10), ClassStableID),
			at:            r.SubmittedAt,
			firstEligible: true,
			workKind:      WorkInspectNewReview,
			title:         fmt.Sprintf("New review on %s #%d", d.Kind, d.Number),
		})
	}
	updatedKey := d.UpdatedAt.UTC().Format(time.RFC3339)
	switch {
	case d.Merged:
		out = append(out, derivedEvent{
			obs:      newObservation(d, EventMerged, updatedKey, ClassSyntheticID),
			at:       d.UpdatedAt,
			workKind: WorkRefreshScope,
			title:    fmt.Sprintf("%s #%d merged", d.Kind, d.Number),
		})
	case d.State == "closed":
		out = append(out, derivedEvent{
			obs:      newObservation(d, EventClosed, updatedKey, ClassSyntheticID),
			at:       d.UpdatedAt,
			workKind: WorkRefreshScope,
			title:    fmt.Sprintf("%s #%d closed", d.Kind, d.Number),
		})
	}
	for _, l := range d.Labels {
		out = append(out, derivedEvent{
			obs:      newObservation(d, EventLabelAdded, strconv.FormatInt(l.ID, 10), ClassSyntheticID),
			at:       d.UpdatedAt,
			workKind: WorkRefreshScope,
			title:    fmt.Sprintf("Label %q on %s #%d", l.Name, d.Kind, d.Number),
		})
	}
	return out
}

func newObservation(d ThreadDetail, eventKind, eventKey, class string) Observation {
	return Observation{
		Source:         Source,
		Repo:           d.Repo,
		ArtifactKind:   d.Kind,
		ArtifactNumber: d.Number,
		EventKind:      eventKind,
		EventKey:       eventKey,
		EventClass:     class,
		SourceQuery:    "thread detail diff",
		PayloadJSON:    "{}",
	}
}

func workItemID(eventID string) string { return "wi:" + eventID }

// isFirstContact reports whether the thread has no prior translation-derived
// observations recorded (the common case at boot, when the unread bootstrap has
// imported the thread but it has not yet been diffed).
func isFirstContact(ctx context.Context, db *sql.DB, repo, kind string, number int64) (bool, error) {
	query := `SELECT COUNT(*) FROM raw_observations WHERE source = ? AND repo = ? AND artifact_kind = ? AND artifact_number = ? AND event_kind IN (?, ?, ?, ?, ?)`
	args := []any{Source, repo, kind, number}
	for _, k := range translationKinds {
		args = append(args, k)
	}
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, fmt.Errorf("first-contact check: %w", err)
	}
	return n == 0, nil
}

// recordObservation inserts a raw_observation keyed on its EventID, returning
// whether it was newly recorded (false = already seen).
func recordObservation(ctx context.Context, db *sql.DB, runID string, obs Observation) (bool, error) {
	var threadID sql.NullString
	if obs.NotificationThreadID != "" {
		threadID = sql.NullString{String: obs.NotificationThreadID, Valid: true}
	}
	r, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO raw_observations
  (id, run_id, source, repo, artifact_kind, artifact_number, notification_thread_id, event_kind, event_key, event_class, source_query, payload_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		obs.EventID(), runID, obs.Source, obs.Repo, obs.ArtifactKind, obs.ArtifactNumber,
		threadID, obs.EventKind, obs.EventKey, obs.EventClass, obs.SourceQuery, obs.PayloadJSON)
	if err != nil {
		return false, fmt.Errorf("record observation %s: %w", obs.EventID(), err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// createWorkItem inserts a pending work item for a translated event (idempotent
// on kind+source_id+scope_generation) and records the initial state transition.
func createWorkItem(ctx context.Context, db *sql.DB, scopeGen int64, eventID, kind, title string) error {
	r, err := db.ExecContext(ctx, `
INSERT OR IGNORE INTO work_items (id, raw_observation_id, kind, source_id, title, scope_generation, state)
VALUES (?, ?, ?, ?, ?, ?, 'pending')`,
		workItemID(eventID), eventID, kind, eventID, title, scopeGen)
	if err != nil {
		return fmt.Errorf("create work item %s: %w", eventID, err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO work_item_state_transitions (work_item_id, to_state, reason)
VALUES (?, 'pending', 'translated from event')`, workItemID(eventID)); err != nil {
		return fmt.Errorf("record work item transition %s: %w", eventID, err)
	}
	return nil
}
