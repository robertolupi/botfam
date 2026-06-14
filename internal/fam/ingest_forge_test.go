package fam

import (
	"errors"
	"path/filepath"
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

func drainOnce(t *testing.T, fc *fakeForge) (mboxPath string) {
	t.Helper()
	mboxPath = filepath.Join(t.TempDir(), "claude.mailbox")
	w, err := mailbox.OpenWriter(mboxPath)
	if err != nil {
		t.Fatal(err)
	}
	p := NewForgePoller(fc, fc.repo)
	err = p.Poll(w, &mailbox.Cursors{})
	w.Close()
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	return mboxPath
}

func TestForgePollerDrainsRepo(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", 3, 1, 2)}
	mboxPath := drainOnce(t, fc)

	evs := forgeEvents(t, mboxPath)
	if len(evs) != 3 {
		t.Fatalf("surfaced %d events, want 3", len(evs))
	}
	// Ascending id within the drained page.
	if evs[0].NotifID != 1 || evs[1].NotifID != 2 || evs[2].NotifID != 3 {
		t.Errorf("surfaced ids %d,%d,%d, want 1,2,3", evs[0].NotifID, evs[1].NotifID, evs[2].NotifID)
	}
	if evs[0].Number != 1 {
		t.Errorf("number = %d, want 1 (parsed from URL)", evs[0].Number)
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
	mboxPath := drainOnce(t, fc)

	evs := forgeEvents(t, mboxPath)
	if len(evs) != 120 {
		t.Fatalf("surfaced %d events, want all 120", len(evs))
	}
	if len(fc.marked) != 120 {
		t.Errorf("marked %d, want 120", len(fc.marked))
	}
	if fc.listCalls != 3 { // 50, 50, 20
		t.Errorf("list calls = %d, want 3 (two full pages + a short page)", fc.listCalls)
	}
}

func TestForgePollerAtLeastOnceOnMarkError(t *testing.T) {
	// Append happens before the upstream ack, so if mark-read fails the thread is
	// already durably in the mailbox (re-surfaced later, never lost).
	fc := &fakeForge{repo: "botfam/botfam", unread: repoNotifs("botfam/botfam", 5), markErr: errors.New("no write:notification scope")}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	w, _ := mailbox.OpenWriter(mboxPath)
	p := NewForgePoller(fc, fc.repo)
	err := p.Poll(w, &mailbox.Cursors{})
	w.Close()
	if err == nil {
		t.Fatal("expected Poll to surface the mark-read error")
	}
	if evs := forgeEvents(t, mboxPath); len(evs) != 1 || evs[0].NotifID != 5 {
		t.Errorf("event not durably appended before the failed ack: %+v", evs)
	}
}

func TestForgePollerListError(t *testing.T) {
	fc := &fakeForge{repo: "botfam/botfam", listErr: errors.New("boom")}
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	w, _ := mailbox.OpenWriter(mboxPath)
	defer w.Close()
	p := NewForgePoller(fc, fc.repo)
	if err := p.Poll(w, &mailbox.Cursors{}); err == nil {
		t.Fatal("expected Poll to surface the list error")
	}
	if len(forgeEvents(t, mboxPath)) != 0 {
		t.Error("surfaced events despite a list error")
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
	mboxPath := filepath.Join(t.TempDir(), "claude.mailbox")
	w, _ := mailbox.OpenWriter(mboxPath)
	p := NewForgePoller(fc, fc.repo)
	err := p.Poll(w, &mailbox.Cursors{})
	w.Close()
	if err == nil {
		t.Fatal("expected the drain cap to error when the unread set never shrinks")
	}
	if fc.listCalls != 3 {
		t.Errorf("list calls = %d, want 3 (the cap)", fc.listCalls)
	}
}
