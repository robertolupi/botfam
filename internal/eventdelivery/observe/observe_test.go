package observe_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	giteasdk "gitea.dev/sdk"
	"github.com/robertolupi/botfam/internal/eventdelivery/observe"
	"github.com/robertolupi/botfam/internal/eventdelivery/store"
	"github.com/robertolupi/botfam/internal/forge"
)

const actor = "agy-bot"

// fakeQuerier is an in-memory Querier for the observation tests.
type fakeQuerier struct {
	issues        []*forge.Issue
	pulls         []*forge.PullRequest
	notifications []forge.Notification
	authErr       error
	calls         int
}

func (f *fakeQuerier) AuthLogin(context.Context) (string, error) {
	f.calls++
	return actor, f.authErr
}
func (f *fakeQuerier) RepoSlug() string { return "botfam/botfam" }
func (f *fakeQuerier) ListOpenIssuesAssignedTo(_ context.Context, who string) ([]*forge.Issue, error) {
	if who != actor {
		return nil, nil
	}
	return f.issues, nil
}
func (f *fakeQuerier) ListOpenPulls(context.Context) ([]*forge.PullRequest, error) {
	return f.pulls, nil
}
func (f *fakeQuerier) ListAllUnreadNotifications(context.Context) ([]forge.Notification, error) {
	return f.notifications, nil
}

func user(login string) *giteasdk.User { return &giteasdk.User{UserName: login} }

func openStore(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.StartRun(ctx, db, "run-1", "session-1"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestStandingRecoversAssignedWorkRegardlessOfWatermark proves the under-wake
// guard: standing work is found by query even when the session watermark is now,
// independent of whether the forge emitted a notification.
func TestStandingRecoversAssignedWorkRegardlessOfWatermark(t *testing.T) {
	ctx := context.Background()
	past := time.Now().Add(-24 * time.Hour)
	q := &fakeQuerier{
		issues: []*forge.Issue{
			{Index: 469, Title: "assigned issue", State: giteasdk.StateOpen, Updated: past},
		},
		pulls: []*forge.PullRequest{
			{Index: 476, Title: "review me", State: giteasdk.StateOpen,
				RequestedReviewers: []*giteasdk.User{user(actor)},
				Head:               &giteasdk.PRBranchInfo{Sha: "deadbeef"}},
			{Index: 477, Title: "mine", State: giteasdk.StateOpen,
				Poster: user(actor),
				Head:   &giteasdk.PRBranchInfo{Sha: "cafef00d"}},
			{Index: 478, Title: "assigned pr", State: giteasdk.StateOpen,
				Assignees: []*giteasdk.User{user(actor)},
				Head:      &giteasdk.PRBranchInfo{Sha: "0ddba11"}},
			{Index: 999, Title: "someone else's", State: giteasdk.StateOpen,
				Poster: user("other-bot"),
				Head:   &giteasdk.PRBranchInfo{Sha: "ffff"}},
		},
	}
	// Watermark = now: every standing artifact predates it.
	o := observe.New(q, observe.NewSessionWatermark(time.Now()))

	got, err := o.Standing(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"forge:botfam/botfam:issue:469:assigned_open:standing":   true,
		"forge:botfam/botfam:pull:476:review_requested:standing": true,
		"forge:botfam/botfam:pull:477:push:cafef00d":             true,
		"forge:botfam/botfam:pull:478:assigned_open:standing":    true,
	}
	gotIDs := map[string]bool{}
	for _, ob := range got {
		gotIDs[ob.EventID()] = true
	}
	for id := range want {
		if !gotIDs[id] {
			t.Errorf("missing standing observation %q; got %v", id, gotIDs)
		}
	}
	if gotIDs["forge:botfam/botfam:pull:999:push:ffff"] {
		t.Errorf("standing leaked another actor's authored PR")
	}
}

// TestStandingIsStableAcrossPolls proves re-polling an unchanged artifact does
// not mint a new id: the query-only standing events keep a constant key.
func TestStandingIsStableAcrossPolls(t *testing.T) {
	ctx := context.Background()
	q := &fakeQuerier{issues: []*forge.Issue{{Index: 469, State: giteasdk.StateOpen}}}
	o := observe.New(q, observe.NewSessionWatermark(time.Now()))

	first, err := o.Standing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := o.Standing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("want 1 observation each poll, got %d and %d", len(first), len(second))
	}
	if first[0].EventID() != second[0].EventID() {
		t.Errorf("standing id changed across polls: %q vs %q", first[0].EventID(), second[0].EventID())
	}
}

// TestBootstrapImportsUnreadRegardlessOfWatermark proves the pre-session unread
// bootstrap rule: an out-of-scope poke whose updated_at predates the session
// start is still imported.
func TestBootstrapImportsUnreadRegardlessOfWatermark(t *testing.T) {
	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	q := &fakeQuerier{notifications: []forge.Notification{
		mkNote(101, true, "Issue", "http://gitea:3000/api/v1/repos/botfam/botfam/issues/300", old),
		mkNote(102, true, "Pull", "http://gitea:3000/api/v1/repos/botfam/botfam/pulls/476", old),
		mkNote(103, false, "Issue", "http://gitea:3000/api/v1/repos/botfam/botfam/issues/301", old), // read -> skip
		mkNote(104, true, "Repository", "http://gitea:3000/api/v1/repos/botfam/botfam", old),        // no artifact -> skip
	}}
	o := observe.New(q, observe.NewSessionWatermark(time.Now()))

	got, err := o.Bootstrap(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, ob := range got {
		ids[ob.EventID()] = true
		if ob.NotificationThreadID == "" {
			t.Errorf("bootstrap observation %q missing thread id", ob.EventID())
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 imported unread threads, got %d: %v", len(got), ids)
	}
	if !ids["forge:botfam/botfam:issue:300:notification:"+old] {
		t.Errorf("missing imported issue notification; got %v", ids)
	}
	if !ids["forge:botfam/botfam:pull:476:notification:"+old] {
		t.Errorf("missing imported pull notification; got %v", ids)
	}
}

func mkNote(id int64, unread bool, subjectType, url, updated string) forge.Notification {
	var n forge.Notification
	n.ID = id
	n.Unread = unread
	n.Updated = updated
	n.Subject.Type = subjectType
	n.Subject.URL = url
	n.Subject.Title = "poke"
	n.Repository.FullName = "botfam/botfam"
	return n
}

// TestWatermarkGatesLiveWakesOnly proves the watermark classifies a live push
// stream while leaving standing/bootstrap untouched.
func TestWatermarkGatesLiveWakesOnly(t *testing.T) {
	start := time.Now()
	o := observe.New(&fakeQuerier{}, observe.NewSessionWatermark(start))

	before := observe.TimedObservation{
		Observation: observe.Observation{Source: observe.Source, Repo: "botfam/botfam", ArtifactKind: observe.KindIssue, ArtifactNumber: 1, EventKind: "comment", EventKey: "a"},
		At:          start.Add(-time.Minute),
	}
	after := observe.TimedObservation{
		Observation: observe.Observation{Source: observe.Source, Repo: "botfam/botfam", ArtifactKind: observe.KindIssue, ArtifactNumber: 2, EventKind: "comment", EventKey: "b"},
		At:          start.Add(time.Minute),
	}

	live := o.LiveWakes([]observe.TimedObservation{before, after})
	if len(live) != 1 || live[0].ArtifactNumber != 2 {
		t.Fatalf("want only the post-start event as a live wake, got %v", live)
	}
}

// TestPollIngestionIsIdempotent proves ingestion dedupes across restarts: the
// same standing/bootstrap set re-ingested produces no new rows.
func TestPollIngestionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openStore(t)
	q := &fakeQuerier{
		issues: []*forge.Issue{{Index: 469, State: giteasdk.StateOpen}},
		notifications: []forge.Notification{
			mkNote(101, true, "Issue", "http://gitea:3000/api/v1/repos/botfam/botfam/issues/300", "2026-06-20T10:00:00Z"),
		},
	}
	o := observe.New(q, observe.NewSessionWatermark(time.Now()))

	obs, err := o.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("want 2 observations from poll, got %d", len(obs))
	}

	first, err := observe.Ingest(ctx, db, "run-1", obs)
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 2 || first.Deduped != 0 {
		t.Fatalf("first ingest: inserted=%d deduped=%d, want 2/0", first.Inserted, first.Deduped)
	}

	// Simulate a restart re-poll: same observations, already-translated.
	obs2, err := o.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := observe.Ingest(ctx, db, "run-1", obs2)
	if err != nil {
		t.Fatal(err)
	}
	if second.Inserted != 0 || second.Deduped != 2 {
		t.Fatalf("re-ingest: inserted=%d deduped=%d, want 0/2", second.Inserted, second.Deduped)
	}

	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM raw_observations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("raw_observations rows = %d, want 2", rows)
	}
}

// TestPollDedupesThreadAlsoStanding proves Poll merges standing and bootstrap
// without duplicate EventIDs.
func TestPollDedupesAcrossSources(t *testing.T) {
	ctx := context.Background()
	q := &fakeQuerier{
		issues:        []*forge.Issue{{Index: 469, State: giteasdk.StateOpen}},
		notifications: []forge.Notification{mkNote(101, true, "Issue", "http://x/repos/botfam/botfam/issues/469", "2026-06-20T10:00:00Z")},
	}
	o := observe.New(q, observe.NewSessionWatermark(time.Now()))
	obs, err := o.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, ob := range obs {
		seen[ob.EventID()]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("duplicate observation %q appeared %d times", id, n)
		}
	}
}
