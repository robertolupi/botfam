package ingest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// fakeForge is a read-only notification source for the watermark poller: it
// returns its unread set (the poller never mutates it — no mark-read). Canned
// timelines/subjects drive the enrichment path; empty maps make GetIssueTimeline/
// GetSubject return nothing so the poller falls back to a URL-only body.
type fakeForge struct {
	repo      string
	unread    []forge.Notification
	listErr   error
	timelines map[int][]*forge.TimelineEvent // keyed by issue/PR number
	subjects  map[string]*forge.SubjectContent
}

func (f *fakeForge) ListUnreadRepoNotifications(_ context.Context, repo string) ([]forge.Notification, error) {
	if repo != f.repo {
		// The poller must call the repo it was built with (server-side scoping).
		return nil, errors.New("unexpected repo " + repo)
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]forge.Notification(nil), f.unread...), nil
}

func (f *fakeForge) GetIssueTimeline(_ context.Context, issueNum int) ([]*forge.TimelineEvent, error) {
	return f.timelines[issueNum], nil
}

func (f *fakeForge) GetSubject(_ context.Context, apiURL string) (*forge.SubjectContent, error) {
	return f.subjects[apiURL], nil
}

// forgeBase anchors deterministic notification updated_at timestamps: a notif
// with id N is updated at forgeBase+N seconds, so higher id == later updated_at
// (the watermark keys on updated_at, not id).
var forgeBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// pastWM is a watermark just above the "unset/legacy" floor — a poll with this
// watermark delivers all notif()s (which are dated in 2026, well after it).
const pastWM = minForgeWatermark + 1

func notif(id int64, repo, typ, url string) forge.Notification {
	var n forge.Notification
	n.ID = id
	n.Repository.FullName = repo
	n.Subject.Type = typ
	n.Subject.Title = "t"
	n.Subject.HTMLURL = url
	n.Updated = forgeBase.Add(time.Duration(id) * time.Second).Format(time.RFC3339)
	return n
}

func repoNotifs(repo string, ids ...int64) []forge.Notification {
	var ns []forge.Notification
	for _, id := range ids {
		ns = append(ns, notif(id, repo, "Issue", "http://gitea:3000/"+repo+"/issues/"+itoa(int(id))))
	}
	return ns
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// forgeMessages reads the forge messages delivered to the spool's new/ box, in
// delivery order.
func forgeMessages(t *testing.T, spoolDir string) []*mailbox.Message {
	t.Helper()
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := sp.ListNew()
	if err != nil {
		t.Fatal(err)
	}
	var out []*mailbox.Message
	for _, e := range ents {
		m, err := sp.Read(e)
		if err != nil {
			t.Fatal(err)
		}
		if m.Source == mailbox.SourceForge {
			out = append(out, m)
		}
	}
	return out
}

// pollSeeded polls once with an already-seeded cursor (watermark 0), so every
// notification with id > 0 is delivered — the path the enrichment tests want.
func pollSeeded(t *testing.T, fc *fakeForge) (spoolDir string) {
	t.Helper()
	spoolDir = filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo, "") // login "" → fail-open (directed=true)
	if err := p.Poll(sp, &mailbox.Cursors{ForgeWatermark: pastWM}); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return spoolDir
}

func TestForgePollerSeedsThenDelivers(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", 3, 1, 2)}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo, "")
	cur := &mailbox.Cursors{} // fresh: not yet seeded

	// First poll seeds the watermark to the current high-water updated_at and
	// delivers nothing (no backlog flood).
	if err := p.Poll(sp, cur); err != nil {
		t.Fatal(err)
	}
	if n := len(forgeMessages(t, spoolDir)); n != 0 {
		t.Fatalf("seed poll delivered %d, want 0 (backlog must not flood)", n)
	}
	if want := forgeBase.Add(3 * time.Second).Unix(); cur.ForgeWatermark != want {
		t.Fatalf("after seed: watermark=%d, want %d (max updated_at of unread)", cur.ForgeWatermark, want)
	}

	// A newer notification arrives → delivered; watermark advances.
	fc.unread = append(fc.unread, notif(5, "botfam/botfam", "Issue", "http://gitea:3000/botfam/botfam/issues/5"))
	if err := p.Poll(sp, cur); err != nil {
		t.Fatal(err)
	}
	msgs := forgeMessages(t, spoolDir)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "#5") {
		t.Fatalf("want only the new #5 delivered, got %+v", msgs)
	}
	if want := forgeBase.Add(5 * time.Second).Unix(); cur.ForgeWatermark != want {
		t.Errorf("watermark = %d, want %d", cur.ForgeWatermark, want)
	}

	// Re-poll with no new activity → nothing re-delivered (dedup).
	if err := p.Poll(sp, cur); err != nil {
		t.Fatal(err)
	}
	if n := len(forgeMessages(t, spoolDir)); n != 1 {
		t.Errorf("re-poll delivered extra messages (total %d, want 1) — watermark dedup failed", n)
	}
}

func TestForgePollerSeedEmptyThenDelivers(t *testing.T) {
	// Seeding when there's nothing unread must still mark the source seeded, so
	// the first real notification afterward is delivered (not mistaken for seed).
	fc := &fakeForge{repo: "botfam/botfam"}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, _ := mailbox.Open(spoolDir)
	p := NewForgePoller(fc, fc.repo, "")
	cur := &mailbox.Cursors{}

	if err := p.Poll(sp, cur); err != nil { // seed on empty
		t.Fatal(err)
	}
	if cur.ForgeWatermark < minForgeWatermark {
		t.Fatalf("empty seed should set a real timestamp watermark, got %d", cur.ForgeWatermark)
	}
	// A notification updated after the seed is delivered (the empty seed didn't
	// swallow the first real one).
	future := notif(1, "botfam/botfam", "Issue", "http://gitea:3000/botfam/botfam/issues/1")
	future.Updated = time.Unix(cur.ForgeWatermark+10, 0).UTC().Format(time.RFC3339)
	fc.unread = []forge.Notification{future}
	if err := p.Poll(sp, cur); err != nil {
		t.Fatal(err)
	}
	if n := len(forgeMessages(t, spoolDir)); n != 1 {
		t.Errorf("first notification after an empty seed delivered %d, want 1", n)
	}
}

func TestForgePollerListError(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", listErr: errors.New("boom")}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo, "")
	if err := p.Poll(sp, &mailbox.Cursors{ForgeWatermark: pastWM}); err == nil {
		t.Fatal("expected Poll to surface the list error")
	}
	if len(forgeMessages(t, spoolDir)) != 0 {
		t.Error("surfaced messages despite a list error")
	}
}

func TestForgePollerNamesTimelineEvents(t *testing.T) {
	mkNotif := func(id int64, num int, title, state string) forge.Notification {
		var n forge.Notification
		n.ID = id
		n.Repository.FullName = "botfam/botfam"
		n.Subject.Type = "Issue"
		n.Subject.Title = title
		n.Subject.State = state
		n.Subject.URL = fmt.Sprintf("http://gitea:3000/api/v1/repos/botfam/botfam/issues/%d", num)
		n.Subject.HTMLURL = fmt.Sprintf("http://gitea:3000/botfam/botfam/issues/%d", num)
		return n
	}
	mkUser := func(login string) *forge.User { return &forge.User{UserName: login} }

	commented := mkNotif(1, 350, "Test issue 2", "open")
	closed := mkNotif(2, 350, "Test issue 2", "closed")

	fc := &fakeForge{
		repo:   "botfam/botfam",
		unread: []forge.Notification{commented, closed},
		timelines: map[int][]*forge.TimelineEvent{
			// Both notifications share issue 350; the newest timeline event is the
			// close, so both must name "closed" — not a stale "commented".
			350: {
				{Type: "comment", Poster: mkUser("rlupi"), Body: "please run date", Created: time.Date(2026, 6, 15, 20, 57, 27, 0, time.UTC)},
				{Type: "close", Poster: mkUser("rlupi"), Created: time.Date(2026, 6, 15, 21, 11, 19, 0, time.UTC)},
			},
		},
	}

	msgs := forgeMessages(t, pollSeeded(t, fc))
	if len(msgs) != 2 {
		t.Fatalf("delivered %d messages, want 2", len(msgs))
	}
	for i, m := range msgs {
		if m.Kind != "issue_closed" {
			t.Errorf("msg %d Kind = %q, want issue_closed", i, m.Kind)
		}
		if m.From != "rlupi" {
			t.Errorf("msg %d From = %q, want rlupi", i, m.From)
		}
		if !strings.Contains(m.Body, "rlupi closed") {
			t.Errorf("msg %d body should name the close event: %q", i, m.Body)
		}
		if m.Subject != `issue_closed: botfam/botfam#350 "Test issue 2"` {
			t.Errorf("msg %d Subject = %q", i, m.Subject)
		}
		if m.Date.Format(time.RFC3339) != "2026-06-15T21:11:19Z" {
			t.Errorf("msg %d Date = %v, want the close event time", i, m.Date)
		}
	}
}

func TestForgePollerFallsBackToSubject(t *testing.T) {
	// No timeline for the thread → fall back to the issue/PR subject body.
	var n forge.Notification
	n.ID = 1
	n.Repository.FullName = "botfam/botfam"
	n.Subject.Type = "Issue"
	n.Subject.Title = "Fresh issue"
	n.Subject.State = "open"
	n.Subject.URL = "http://gitea:3000/api/v1/repos/botfam/botfam/issues/351"
	n.Subject.HTMLURL = "http://gitea:3000/botfam/botfam/issues/351"

	sc := &forge.SubjectContent{Title: "Fresh issue", Body: "do the thing", State: "open"}
	sc.User.Login = "rlupi"
	fc := &fakeForge{
		repo:     "botfam/botfam",
		unread:   []forge.Notification{n},
		subjects: map[string]*forge.SubjectContent{n.Subject.URL: sc},
	}
	msgs := forgeMessages(t, pollSeeded(t, fc))
	if len(msgs) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(msgs))
	}
	if msgs[0].Kind != "issue" || msgs[0].From != "rlupi" || !strings.Contains(msgs[0].Body, "do the thing") {
		t.Errorf("subject fallback not applied: Kind=%q From=%q body=%q", msgs[0].Kind, msgs[0].From, msgs[0].Body)
	}
}

func TestForgePollerMarksDirected(t *testing.T) {
	mk := func(id int64, num int) forge.Notification {
		var n forge.Notification
		n.ID = id
		n.Repository.FullName = "botfam/botfam"
		n.Subject.Type = "Issue"
		n.Subject.Title = "t"
		n.Subject.State = "open"
		n.Subject.URL = fmt.Sprintf("http://gitea:3000/api/v1/repos/botfam/botfam/issues/%d", num)
		n.Subject.HTMLURL = fmt.Sprintf("http://gitea:3000/botfam/botfam/issues/%d", num)
		return n
	}
	mkUser2 := func(login string) *forge.User { return &forge.User{UserName: login} }
	assigned := mk(1, 10)  // claude-bot is an assignee → directed
	mentioned := mk(2, 11) // latest comment @-mentions claude-bot → directed
	neither := mk(3, 12)   // someone else's thread, no mention → not directed

	assignedSubj := &forge.SubjectContent{State: "open"}
	assignedSubj.Assignees = []struct {
		Login string `json:"login"`
	}{{Login: "claude-bot"}}
	plainSubj := &forge.SubjectContent{State: "open"}

	fc := &fakeForge{
		repo:   "botfam/botfam",
		unread: []forge.Notification{assigned, mentioned, neither},
		subjects: map[string]*forge.SubjectContent{
			assigned.Subject.URL:  assignedSubj,
			mentioned.Subject.URL: plainSubj,
			neither.Subject.URL:   plainSubj,
		},
		timelines: map[int][]*forge.TimelineEvent{
			11: {{Type: "comment", Poster: mkUser2("rlupi"), Body: "ping @claude-bot please", Created: time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)}},
			12: {{Type: "comment", Poster: mkUser2("agy-bot"), Body: "agy and rlupi discussing", Created: time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)}},
		},
	}

	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, _ := mailbox.Open(spoolDir)
	p := NewForgePoller(fc, fc.repo, "claude-bot")
	if err := p.Poll(sp, &mailbox.Cursors{ForgeWatermark: pastWM}); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{} // subject ref -> directed
	for _, m := range forgeMessages(t, spoolDir) {
		got[m.Subject] = m.Directed
	}
	for ref, wantDirected := range map[string]bool{
		"#10": true, "#11": true, "#12": false,
	} {
		var found, directed bool
		for subj, d := range got {
			if strings.Contains(subj, ref) {
				found, directed = true, d
			}
		}
		if !found {
			t.Errorf("%s not delivered", ref)
		} else if directed != wantDirected {
			t.Errorf("%s directed=%v, want %v", ref, directed, wantDirected)
		}
	}
}
