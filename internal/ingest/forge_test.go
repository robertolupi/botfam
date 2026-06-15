package ingest

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// fakeForge models the repo-scoped unread set as a draining queue: a successful
// MarkNotificationRead removes the thread, so ListUnreadRepoNotifications returns
// the next page on the following call. neverAck=true makes mark-read a no-op so
// the set never shrinks (to exercise the drain cap).
type fakeForge struct {
	repo      string
	unread    []forge.Notification
	marked    []int64
	listCalls int
	listErr   error
	markErr   error
	neverAck  bool
	// Canned enrichment content keyed by API URL; empty maps make GetSubject/
	// GetComment return (nil, nil) so the poller falls back to a URL-only body.
	subjects map[string]*forge.SubjectContent
	comments map[string]*forge.Comment
}

func (f *fakeForge) GetSubject(apiURL string) (*forge.SubjectContent, error) {
	return f.subjects[apiURL], nil
}

func (f *fakeForge) GetComment(apiURL string) (*forge.Comment, error) {
	return f.comments[apiURL], nil
}

func (f *fakeForge) ListUnreadRepoNotifications(repo string) ([]forge.Notification, error) {
	f.listCalls++
	if repo != f.repo {
		// The poller must call the repo it was built with (server-side scoping).
		return nil, errors.New("unexpected repo " + repo)
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	limit := forge.NotificationsPageLimit()
	n := len(f.unread)
	if n > limit {
		n = limit
	}
	return append([]forge.Notification(nil), f.unread[:n]...), nil
}

func (f *fakeForge) MarkNotificationRead(id int64) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, id)
	if f.neverAck {
		return nil
	}
	out := f.unread[:0]
	for _, n := range f.unread {
		if n.ID != id {
			out = append(out, n)
		}
	}
	f.unread = out
	return nil
}

func notif(id int64, repo, typ, url string) forge.Notification {
	var n forge.Notification
	n.ID = id
	n.Repository.FullName = repo
	n.Subject.Type = typ
	n.Subject.Title = "t"
	n.Subject.HTMLURL = url
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

func drainOnce(t *testing.T, fc *fakeForge) (spoolDir string) {
	t.Helper()
	spoolDir = filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo)
	if err := p.Poll(sp, &mailbox.Cursors{}); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return spoolDir
}

func TestForgePollerDrainsRepo(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", 3, 1, 2)}
	spoolDir := drainOnce(t, fc)

	msgs := forgeMessages(t, spoolDir)
	if len(msgs) != 3 {
		t.Fatalf("surfaced %d messages, want 3", len(msgs))
	}
	// Ascending id within the drained page: delivery order is the sort order.
	for i, want := range []int{1, 2, 3} {
		if !strings.Contains(msgs[i].Subject, fmt.Sprintf("#%d", want)) {
			t.Errorf("message %d subject = %q, want artifact ref #%d", i, msgs[i].Subject, want)
		}
		if !strings.HasSuffix(msgs[i].Body, fmt.Sprintf("/issues/%d", want)) {
			t.Errorf("message %d body = %q, want url ending /issues/%d", i, msgs[i].Body, want)
		}
	}
	if len(fc.marked) != 3 {
		t.Errorf("marked %d threads read, want 3", len(fc.marked))
	}
	if len(fc.unread) != 0 {
		t.Errorf("unread set not drained: %d remain", len(fc.unread))
	}
}

func TestForgePollerDrainsAcrossPages(t *testing.T) {
	// 120 unread > 2 pages of 50: the drain must surface all of them, with no
	// offset and no cursor — always re-fetching the (shrinking) first page.
	var ids []int64
	for id := int64(1); id <= 120; id++ {
		ids = append(ids, id)
	}
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", ids...)}
	spoolDir := drainOnce(t, fc)

	msgs := forgeMessages(t, spoolDir)
	if len(msgs) != 120 {
		t.Fatalf("surfaced %d messages, want all 120", len(msgs))
	}
	if len(fc.marked) != 120 {
		t.Errorf("marked %d, want 120", len(fc.marked))
	}
	if fc.listCalls != 3 { // 50, 50, 20
		t.Errorf("list calls = %d, want 3 (two full pages + a short page)", fc.listCalls)
	}
}

func TestForgePollerAtLeastOnceOnMarkError(t *testing.T) {
	// Deliver happens before the upstream ack, so if mark-read fails the thread is
	// already durably in the spool (re-surfaced later, never lost).
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", 5), markErr: errors.New("no write:notification scope")}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo)
	if err := p.Poll(sp, &mailbox.Cursors{}); err == nil {
		t.Fatal("expected Poll to surface the mark-read error")
	}
	msgs := forgeMessages(t, spoolDir)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Subject, "#5") {
		t.Errorf("message not durably delivered before the failed ack: %+v", msgs)
	}
}

func TestForgePollerListError(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", listErr: errors.New("boom")}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo)
	if err := p.Poll(sp, &mailbox.Cursors{}); err == nil {
		t.Fatal("expected Poll to surface the list error")
	}
	if len(forgeMessages(t, spoolDir)) != 0 {
		t.Error("surfaced messages despite a list error")
	}
}

func TestForgePollerDrainCapErrors(t *testing.T) {
	defer func(orig int) { maxForgeDrainPages = orig }(maxForgeDrainPages)
	maxForgeDrainPages = 3

	// A full page that never shrinks (ack is a no-op) must hit the cap and error
	// rather than loop forever.
	var ids []int64
	for id := int64(1); id <= int64(forge.NotificationsPageLimit()); id++ {
		ids = append(ids, id)
	}
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", ids...), neverAck: true}
	spoolDir := filepath.Join(t.TempDir(), "spool")
	sp, err := mailbox.Open(spoolDir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo)
	if err := p.Poll(sp, &mailbox.Cursors{}); err == nil {
		t.Fatal("expected the drain cap to error when the unread set never shrinks")
	}
	if fc.listCalls != 3 {
		t.Errorf("list calls = %d, want 3 (the cap)", fc.listCalls)
	}
}

func TestForgePollerEnrichesCommentAndSubject(t *testing.T) {
	// One comment event (latest_comment_url set) and one bare open event.
	var comment, opened forge.Notification
	comment.ID = 1
	comment.Repository.FullName = "botfam/botfam"
	comment.Subject.Type = "Issue"
	comment.Subject.Title = "Test issue 2"
	comment.Subject.State = "open"
	comment.Subject.URL = "http://gitea:3000/api/v1/repos/botfam/botfam/issues/350"
	comment.Subject.HTMLURL = "http://gitea:3000/botfam/botfam/issues/350"
	comment.Subject.LatestCommentURL = "http://gitea:3000/api/v1/repos/botfam/botfam/issues/comments/9"
	comment.Updated = "2026-06-15T20:57:27Z"

	opened.ID = 2
	opened.Repository.FullName = "botfam/botfam"
	opened.Subject.Type = "Issue"
	opened.Subject.Title = "Fresh issue"
	opened.Subject.State = "open"
	opened.Subject.URL = "http://gitea:3000/api/v1/repos/botfam/botfam/issues/351"
	opened.Subject.HTMLURL = "http://gitea:3000/botfam/botfam/issues/351"

	fc := &fakeForge{
		repo:   "botfam/botfam",
		unread: []forge.Notification{comment, opened},
		comments: map[string]*forge.Comment{
			comment.Subject.LatestCommentURL: {
				Body:    "please run date",
				HTMLURL: "http://gitea:3000/botfam/botfam/issues/350#issuecomment-9",
				User:    struct{ Login string `json:"login"` }{Login: "rlupi"},
			},
		},
		subjects: map[string]*forge.SubjectContent{
			opened.Subject.URL: {Title: "Fresh issue", Body: "do the thing", State: "open",
				User: struct{ Login string `json:"login"` }{Login: "rlupi"}},
		},
	}

	spoolDir := drainOnce(t, fc)
	msgs := forgeMessages(t, spoolDir)
	if len(msgs) != 2 {
		t.Fatalf("delivered %d messages, want 2", len(msgs))
	}
	c, o := msgs[0], msgs[1]

	if c.Kind != "issue_comment" {
		t.Errorf("comment Kind = %q, want issue_comment", c.Kind)
	}
	if c.From != "rlupi" {
		t.Errorf("comment From = %q, want rlupi", c.From)
	}
	if !strings.Contains(c.Body, "please run date") {
		t.Errorf("comment body missing comment text: %q", c.Body)
	}
	if c.Subject != `issue_comment: botfam/botfam#350 "Test issue 2"` {
		t.Errorf("comment Subject = %q", c.Subject)
	}
	if c.Date.IsZero() {
		t.Error("comment Date should be the event time, not zero")
	}

	if o.Kind != "issue" {
		t.Errorf("open Kind = %q, want issue", o.Kind)
	}
	if !strings.Contains(o.Body, "do the thing") || o.From != "rlupi" {
		t.Errorf("open event not enriched from subject: From=%q body=%q", o.From, o.Body)
	}
}
