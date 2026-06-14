package fam

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// ForgeClient is the slice of the forge client the ingester needs; an interface
// so the poller is testable with a fake.
type ForgeClient interface {
	// ListUnreadRepoNotifications returns one page of unread threads scoped to
	// repo (owner/repo).
	ListUnreadRepoNotifications(repo string) ([]forge.Notification, error)
	MarkNotificationRead(id int64) error
}

// maxForgeDrainPages caps a single Poll's drain so a thread that refuses to ack
// (e.g. a token without write:notification) cannot loop forever. A var so tests
// can shrink it.
var maxForgeDrainPages = 200

// forgePoller surfaces Gitea notifications as forge events by *draining* the
// repo's unread set: each poll repeatedly fetches a page of unread threads for
// the fam's repo, appends each to the mailbox, then marks it read, until the
// unread set is empty.
//
// This avoids the offset-pagination + id-high-water-cursor design and its
// skip/data-loss hazards (#251 review): there is no offset to shift under a
// mutating list and no cursor to advance past an unseen thread. It is
// at-least-once — the mailbox append happens *before* the upstream ack, so a
// crash in between re-surfaces the thread (a benign duplicate) rather than
// losing it. Marking read is the consumption mechanism; the mailbox is the
// durable record, so acking upstream loses nothing. A thread that gets new
// activity later re-appears as unread and is surfaced again — which also fixes
// the updated-thread gap (#253).
type forgePoller struct {
	client ForgeClient
	repo   string // owner/repo
}

// NewForgePoller builds the forge source draining repo (owner/repo).
func NewForgePoller(client ForgeClient, repo string) Poller {
	return &forgePoller{client: client, repo: repo}
}

func (p *forgePoller) Name() string { return mailbox.SourceForge }

func (p *forgePoller) Poll(w *mailbox.Writer, _ *mailbox.Cursors) error {
	limit := forge.NotificationsPageLimit()
	for page := 0; page < maxForgeDrainPages; page++ {
		ns, err := p.client.ListUnreadRepoNotifications(p.repo)
		if err != nil {
			return err
		}
		if len(ns) == 0 {
			return nil // drained
		}
		// Ascending id for a stable surfacing order within a page.
		sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
		for _, n := range ns {
			url := n.Subject.HTMLURL
			if url == "" {
				url = n.Subject.URL
			}
			// Mailbox append first, then ack: at-least-once, never lose a thread.
			if _, err := w.Append(mailbox.Event{
				Source:      mailbox.SourceForge,
				SubjectType: n.Subject.Type,
				Repo:        n.Repository.FullName,
				Number:      numberFromURL(url),
				Title:       n.Subject.Title,
				URL:         url,
				NotifID:     n.ID,
			}); err != nil {
				return err
			}
			if err := p.client.MarkNotificationRead(n.ID); err != nil {
				return err
			}
		}
		if len(ns) < limit {
			return nil // last (partial) page drained
		}
	}
	return fmt.Errorf("forge: repo %s unread did not drain after %d pages (notification ack failing?)", p.repo, maxForgeDrainPages)
}

// numberFromURL extracts the trailing issue/PR number from a subject URL, or 0.
func numberFromURL(url string) int64 {
	url = strings.TrimRight(url, "/")
	if i := strings.LastIndex(url, "/"); i >= 0 {
		if n, err := strconv.ParseInt(url[i+1:], 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// ForgePollerFor builds the forge source for the agent owning workDir, scoped to
// the fam's repository. It errors when the fam declares no repository or the
// forge client can't be built (e.g. no token), so the caller can run IRC-only.
func ForgePollerFor(workDir, actor string) (Poller, error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return nil, err
	}
	if rf.Repository == "" {
		return nil, fmt.Errorf("fam %q declares no repository; forge ingest disabled", rf.Slug)
	}
	client, err := forge.NewClient(workDir, actor)
	if err != nil {
		return nil, err
	}
	return NewForgePoller(client, rf.Repository), nil
}
