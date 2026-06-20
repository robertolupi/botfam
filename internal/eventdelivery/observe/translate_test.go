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
	// comments 1,2 + label 7 seeded as seen, not dispatched.
	if res.SeededSeen != 3 {
		t.Fatalf("SeededSeen = %d, want 3", res.SeededSeen)
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
