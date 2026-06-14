package fam

import (
	"sort"
	"strconv"
	"strings"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mailbox"
)

// ForgeClient is the slice of the forge client the ingester needs; an interface
// so the poller is testable with a fake. It enumerates the *full* unread set
// (all pages) because the poller advances a high-water cursor and must not skip
// past notifications it never saw (#251 review).
type ForgeClient interface {
	ListAllUnreadNotifications() ([]forge.Notification, error)
	MarkNotificationRead(id int64) error
}

// forgePoller surfaces Gitea notifications as forge events, edge-triggered on the
// notification id and scoped to a single repo so a Gitea account shared across
// fams never cross-wakes (proposal §3.3).
type forgePoller struct {
	client   ForgeClient
	repo     string // owner/repo; empty disables the repo filter
	markRead bool
}

// NewForgePoller builds the forge source. repo is the fam's owner/repo; events
// for any other repo are dropped. markRead acks surfaced threads upstream.
func NewForgePoller(client ForgeClient, repo string, markRead bool) Poller {
	return &forgePoller{client: client, repo: repo, markRead: markRead}
}

func (p *forgePoller) Name() string { return mailbox.SourceForge }

func (p *forgePoller) Poll(w *mailbox.Writer, c *mailbox.Cursors) error {
	// Enumerate the full unread set (all pages): the cursor below advances to the
	// max id seen, so seeing only page 1 would jump the high-water mark past
	// unenumerated same-repo notifications and skip them forever (#251 review).
	ns, err := p.client.ListAllUnreadNotifications()
	if err != nil {
		return err
	}
	// Ascending id so we append in order and advance the cursor monotonically.
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })

	maxID := c.ForgeLastNotificationID
	for _, n := range ns {
		if n.ID > maxID {
			maxID = n.ID
		}
		if n.ID <= c.ForgeLastNotificationID {
			continue // edge-triggered: already past this notification
		}
		if p.repo != "" && n.Repository.FullName != p.repo {
			continue // belongs to another fam's mailbox
		}
		url := n.Subject.HTMLURL
		if url == "" {
			url = n.Subject.URL
		}
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
		if p.markRead {
			_ = p.client.MarkNotificationRead(n.ID)
		}
	}
	// Advance past every unread id we saw (including other-repo ones) so they are
	// not rescanned; another fam's ingester tracks its own cursor in its mailbox.
	c.ForgeLastNotificationID = maxID
	return nil
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
// the fam's repository. It returns an error if the forge client can't be built
// (e.g. no token) so the caller can run IRC-only.
func ForgePollerFor(workDir, actor string, markRead bool) (Poller, error) {
	rf, err := famconfig.ResolveFam(workDir)
	if err != nil {
		return nil, err
	}
	client, err := forge.NewClient(workDir, actor)
	if err != nil {
		return nil, err
	}
	return NewForgePoller(client, rf.Repository, markRead), nil
}
