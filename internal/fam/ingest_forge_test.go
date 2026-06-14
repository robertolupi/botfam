package fam

import (
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

type fakeForge struct {
	notifs []forge.Notification
	marked []int64
}

func (f *fakeForge) ListUnreadNotifications() ([]forge.Notification, error) { return f.notifs, nil }
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
