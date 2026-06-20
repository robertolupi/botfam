package observe_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/eventdelivery/observe"
)

// fakeDetail is an in-memory DetailQuerier for translation/gap-poll/gate tests.
type fakeDetail struct {
	repo     string
	detail   map[int64]observe.ThreadDetail
	blockers map[int64][]observe.Blocker
	status   map[string]string // sha -> combined state
}

func newFakeDetail() *fakeDetail {
	return &fakeDetail{
		repo:     "botfam/botfam",
		detail:   map[int64]observe.ThreadDetail{},
		blockers: map[int64][]observe.Blocker{},
		status:   map[string]string{},
	}
}

func (f *fakeDetail) RepoSlug() string { return f.repo }
func (f *fakeDetail) FetchThreadDetail(_ context.Context, kind string, number int64) (observe.ThreadDetail, error) {
	d := f.detail[number]
	d.Repo = f.repo
	d.Kind = kind
	d.Number = number
	return d, nil
}
func (f *fakeDetail) ListBlockers(_ context.Context, number int64) ([]observe.Blocker, error) {
	return f.blockers[number], nil
}
func (f *fakeDetail) CombinedStatusState(_ context.Context, ref string) (string, error) {
	return f.status[ref], nil
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestTranslateFirstContactSeedsBaseline proves the first import of a populated
// thread records its history as already-seen and dispatches only post-baseline
// comments — no backlog flood.
func TestTranslateFirstContactSeedsBaseline(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	baseline := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)

	q := newFakeDetail()
	q.detail[476] = observe.ThreadDetail{
		State:     "open",
		UpdatedAt: baseline.Add(-48 * time.Hour),
		Comments: []observe.CommentRef{
			{ID: 1, UpdatedAt: baseline.Add(-10 * time.Hour)}, // pre-baseline → seed
			{ID: 2, UpdatedAt: baseline.Add(-2 * time.Hour)},  // pre-baseline → seed
			{ID: 3, UpdatedAt: baseline.Add(1 * time.Hour)},   // post-baseline → dispatch
		},
		Labels: []observe.LabelRef{{ID: 7, Name: "epic"}}, // synthetic → seed, never on first contact
	}
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(baseline))

	res, err := tr.Translate(ctx, db, "run-1", 1, observe.ThreadRef{Kind: observe.KindPull, Number: 476})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FirstSeen {
		t.Fatal("expected FirstSeen on first translation")
	}
	if len(res.Emitted) != 1 || res.Emitted[0].EventID != "forge:botfam/botfam:pull:476:comment:3" {
		t.Fatalf("want only comment:3 dispatched, got %+v", res.Emitted)
	}
	// comments 1,2 seeded as seen, not dispatched (the label and state are
	// seeded separately as scalars, not counted here).
	if res.SeededSeen != 2 {
		t.Fatalf("SeededSeen = %d, want 2", res.SeededSeen)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items`); got != 1 {
		t.Fatalf("work_items = %d, want 1 (no history flood)", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM raw_observations WHERE event_kind = 'comment'`); got != 3 {
		t.Fatalf("recorded comment observations = %d, want 3 (all seeded)", got)
	}
}

// TestTranslateDiffSurvivesRestartAndSiblings proves event-level diffing: two
// events on one thread both surface, a restart re-dispatches nothing, and a new
// sibling still surfaces without hiding the earlier ones.
func TestTranslateDiffSurvivesRestartAndSiblings(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	baseline := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	q := newFakeDetail()
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(baseline))

	// Two post-baseline comments on first contact → both surface (event-level).
	q.detail[476] = observe.ThreadDetail{
		State:     "open",
		UpdatedAt: baseline.Add(time.Hour),
		Comments: []observe.CommentRef{
			{ID: 10, UpdatedAt: baseline.Add(time.Hour)},
			{ID: 11, UpdatedAt: baseline.Add(2 * time.Hour)},
		},
	}
	res, err := tr.Translate(ctx, db, "run-1", 1, observe.ThreadRef{Kind: observe.KindPull, Number: 476})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 2 {
		t.Fatalf("first pass: want 2 events surfaced, got %d", len(res.Emitted))
	}

	// Restart: same detail re-translated → nothing re-dispatched.
	res, err = tr.Translate(ctx, db, "run-1", 1, observe.ThreadRef{Kind: observe.KindPull, Number: 476})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 0 || res.Suppressed != 2 {
		t.Fatalf("restart: want 0 emitted / 2 suppressed, got %d / %d", len(res.Emitted), res.Suppressed)
	}

	// A new sibling comment arrives → only it surfaces; the earlier two are kept,
	// not hidden.
	d := q.detail[476]
	d.Comments = append(d.Comments, observe.CommentRef{ID: 12, UpdatedAt: baseline.Add(3 * time.Hour)})
	q.detail[476] = d
	res, err = tr.Translate(ctx, db, "run-1", 1, observe.ThreadRef{Kind: observe.KindPull, Number: 476})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 1 || res.Emitted[0].EventID != "forge:botfam/botfam:pull:476:comment:12" {
		t.Fatalf("sibling: want only comment:12, got %+v", res.Emitted)
	}
	if res.Suppressed != 2 {
		t.Fatalf("sibling: want 2 prior comments suppressed, got %d", res.Suppressed)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items`); got != 3 {
		t.Fatalf("work_items total = %d, want 3", got)
	}
}

// TestTranslateStateChangeDiffsByValueNotTimestamp proves state transitions are
// keyed by the state value, not the thread's updated_at: an unrelated update on
// an open or already-closed thread does not mint a spurious close/merge refresh.
func TestTranslateStateChangeDiffsByValueNotTimestamp(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	baseline := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	q := newFakeDetail()
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(baseline))
	ref := observe.ThreadRef{Kind: observe.KindIssue, Number: 469}

	set := func(state string, upd time.Time, comments []observe.CommentRef) {
		q.detail[469] = observe.ThreadDetail{State: state, UpdatedAt: upd, Comments: comments}
	}

	// First contact, open → state seeded, nothing dispatched.
	set("open", baseline.Add(-time.Hour), nil)
	if _, err := tr.Translate(ctx, db, "run-1", 1, ref); err != nil {
		t.Fatal(err)
	}
	// Unrelated update advances updated_at but state is still open → no refresh.
	set("open", baseline.Add(time.Hour), nil)
	res, err := tr.Translate(ctx, db, "run-1", 1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 0 {
		t.Fatalf("unrelated update on open issue emitted %+v, want none", res.Emitted)
	}
	// Genuine close → one refresh.
	set("closed", baseline.Add(2*time.Hour), nil)
	res, err = tr.Translate(ctx, db, "run-1", 1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 1 {
		t.Fatalf("close transition emitted %d, want 1 refresh", len(res.Emitted))
	}
	// Comment on the already-closed issue advances updated_at again: the comment
	// dispatches, but the close must NOT re-fire.
	set("closed", baseline.Add(4*time.Hour), []observe.CommentRef{{ID: 90, UpdatedAt: baseline.Add(3 * time.Hour)}})
	if _, err := tr.Translate(ctx, db, "run-1", 1, ref); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items WHERE kind = ?`, observe.WorkRefreshScope); got != 1 {
		t.Fatalf("refresh_scope work items = %d, want 1 (no spurious re-close)", got)
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items WHERE kind = ?`, observe.WorkInspectNewComment); got != 1 {
		t.Fatalf("comment work items = %d, want 1", got)
	}
}

// TestTranslateLabelSetAddAndRemove proves label changes are diffed as a set:
// both additions and removals trigger a refresh (not just additions).
func TestTranslateLabelSetAddAndRemove(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	q := newFakeDetail()
	tr := observe.NewTranslator(q, observe.NewSessionWatermark(time.Now()))
	ref := observe.ThreadRef{Kind: observe.KindPull, Number: 476}

	set := func(ids ...int64) {
		labels := make([]observe.LabelRef, 0, len(ids))
		for _, id := range ids {
			labels = append(labels, observe.LabelRef{ID: id})
		}
		q.detail[476] = observe.ThreadDetail{State: "open", Labels: labels}
	}

	set(1, 2) // first contact → seeded, no refresh
	if _, err := tr.Translate(ctx, db, "run-1", 1, ref); err != nil {
		t.Fatal(err)
	}
	set(1, 2) // unchanged → no refresh
	res, err := tr.Translate(ctx, db, "run-1", 1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 0 {
		t.Fatalf("unchanged label set emitted %+v, want none", res.Emitted)
	}
	set(1, 2, 3) // addition → refresh
	res, err = tr.Translate(ctx, db, "run-1", 1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 1 {
		t.Fatalf("label addition emitted %d, want 1", len(res.Emitted))
	}
	set(2, 3) // removal of #1 → refresh (the bug: removals were previously missed)
	res, err = tr.Translate(ctx, db, "run-1", 1, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Emitted) != 1 {
		t.Fatalf("label removal emitted %d, want 1", len(res.Emitted))
	}
	if got := countRows(t, db, `SELECT COUNT(*) FROM work_items WHERE kind = ?`, observe.WorkRefreshScope); got != 2 {
		t.Fatalf("refresh_scope work items = %d, want 2 (one add, one remove)", got)
	}
}
