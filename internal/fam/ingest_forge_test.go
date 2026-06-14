package fam

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

type fakeForge struct {
	notifs []forge.Notification
	marked []int64
	err    error
}

func (f *fakeForge) ListAllUnreadNotifications() ([]forge.Notification, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.notifs, nil
}
func (f *fakeForge) MarkNotificationRead(id int64) error {
	f.marked = append(f.marked, id)
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

func forgeEvents(t *testing.T, mboxPath string) []mailbox.Event {
	t.Helper()
	evs, _, err := mailbox.ReadFrom(mboxPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []mailbox.Event
	for _, e := range evs {
		if e.Source == mailbox.SourceForge {
			out = append(out, e)
		}
	}
	return out
}

func TestForgePollerRepoScopedAndEdgeTriggered(t *testing.T) {
	fc := &fakeForge{notifs: []forge.Notification{
		notif(1, "botfam/botfam", "Pull", "http://gitea:3000/botfam/botfam/pulls/230"),
		notif(2, "deepcuts/deepcuts", "Issue", "http://gitea:3000/deepcuts/deepcuts/issues/9"),
		notif(3, "botfam/botfam", "Issue", "http://gitea:3000/botfam/botfam/issues/240"),
	}}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	p := NewForgePoller(fc, "botfam/botfam", false)

	w, err := mailbox.OpenWriter(mboxPath)
	if err != nil {
		t.Fatal(err)
	}
	var cur mailbox.Cursors
	if err := p.Poll(w, &cur); err != nil {
		t.Fatal(err)
	}
	w.Close()

	evs := forgeEvents(t, mboxPath)
	if len(evs) != 2 {
		t.Fatalf("surfaced %d forge events, want 2 (deepcuts filtered out)", len(evs))
	}
	if evs[0].NotifID != 1 || evs[1].NotifID != 3 {
		t.Errorf("surfaced notif ids = %d,%d, want 1,3", evs[0].NotifID, evs[1].NotifID)
	}
	if evs[0].Number != 230 {
		t.Errorf("number = %d, want 230 (parsed from URL)", evs[0].Number)
	}
	// Cursor advanced past every unread id seen, including the filtered deepcuts
	// one, so it is never rescanned.
	if cur.ForgeLastNotificationID != 3 {
		t.Errorf("cursor = %d, want 3", cur.ForgeLastNotificationID)
	}

	// Second poll over the same set is edge-triggered: nothing new.
	w2, _ := mailbox.OpenWriter(mboxPath)
	before := len(forgeEvents(t, mboxPath))
	if err := p.Poll(w2, &cur); err != nil {
		t.Fatal(err)
	}
	w2.Close()
	if after := len(forgeEvents(t, mboxPath)); after != before {
		t.Errorf("edge-trigger re-surfaced notifications: %d -> %d", before, after)
	}
}

// TestForgePollerNoSkipBeyondOnePage guards the #251 review fix: with more unread
// same-repo notifications than a single API page, none may be skipped. The fake
// satisfies the full-enumeration contract (ListAllUnreadNotifications), so every
// id > cursor must surface and the cursor must land on the global max.
func TestForgePollerNoSkipBeyondOnePage(t *testing.T) {
	var notifs []forge.Notification
	for id := int64(1); id <= 120; id++ { // > 2 pages of 50
		notifs = append(notifs, notif(id, "botfam/botfam", "Issue",
			"http://gitea:3000/botfam/botfam/issues/"+strconv.FormatInt(id, 10)))
	}
	fc := &fakeForge{notifs: notifs}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	p := NewForgePoller(fc, "botfam/botfam", false)

	w, _ := mailbox.OpenWriter(mboxPath)
	var cur mailbox.Cursors
	if err := p.Poll(w, &cur); err != nil {
		t.Fatal(err)
	}
	w.Close()

	evs := forgeEvents(t, mboxPath)
	if len(evs) != 120 {
		t.Fatalf("surfaced %d events, want all 120 (no skip beyond page 1)", len(evs))
	}
	if evs[0].NotifID != 1 || evs[len(evs)-1].NotifID != 120 {
		t.Errorf("surfaced range %d..%d, want 1..120", evs[0].NotifID, evs[len(evs)-1].NotifID)
	}
	if cur.ForgeLastNotificationID != 120 {
		t.Errorf("cursor = %d, want 120", cur.ForgeLastNotificationID)
	}
}

// TestForgePollerDoesNotAdvanceOnIncompleteScan guards the cap-boundary case
// from the #251 re-review: if the client can't enumerate the full unread set
// (e.g. it exceeded the page cap), Poll must surface the error and leave the
// cursor untouched, so a later poll retries rather than skipping the tail.
func TestForgePollerDoesNotAdvanceOnIncompleteScan(t *testing.T) {
	fc := &fakeForge{err: errors.New("unread notifications exceed the page cap")}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	w, _ := mailbox.OpenWriter(mboxPath)
	defer w.Close()

	p := NewForgePoller(fc, "botfam/botfam", false)
	cur := mailbox.Cursors{ForgeLastNotificationID: 42}
	if err := p.Poll(w, &cur); err == nil {
		t.Fatal("expected Poll to surface the incomplete-scan error")
	}
	if cur.ForgeLastNotificationID != 42 {
		t.Errorf("cursor advanced to %d on an incomplete scan, want 42 (unchanged)", cur.ForgeLastNotificationID)
	}
	if len(forgeEvents(t, mboxPath)) != 0 {
		t.Error("surfaced events from an incomplete scan")
	}
}

func TestForgePollerMarkRead(t *testing.T) {
	fc := &fakeForge{notifs: []forge.Notification{
		notif(7, "botfam/botfam", "Pull", "http://gitea:3000/botfam/botfam/pulls/1"),
	}}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	w, _ := mailbox.OpenWriter(mboxPath)
	defer w.Close()

	p := NewForgePoller(fc, "botfam/botfam", true)
	var cur mailbox.Cursors
	if err := p.Poll(w, &cur); err != nil {
		t.Fatal(err)
	}
	if len(fc.marked) != 1 || fc.marked[0] != 7 {
		t.Errorf("marked = %v, want [7]", fc.marked)
	}
}
