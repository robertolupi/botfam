package ingest

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// GetSubject fetches the issue/PR behind a notification subject (title, body,
	// state, author); GetComment fetches the comment at a notification's
	// latest_comment_url. Both enrich the spool body so `botfam wait` shows what
	// happened — best-effort: the poller falls back to a URL-only body on error.
	GetSubject(apiURL string) (*forge.SubjectContent, error)
	GetComment(apiURL string) (*forge.Comment, error)
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

func (p *forgePoller) Poll(s *mailbox.Spool, _ *mailbox.Cursors) error {
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
			// Spool deliver first, then ack: at-least-once, never lose a thread.
			if _, err := s.Deliver(p.buildMessage(n)); err != nil {
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

// buildMessage turns a forge notification into a spool message, enriched so
// `botfam wait` shows what happened — not just that something did. It refines
// the Kind (issue vs issue_comment / pull vs pull_comment / pull_review), sets
// From to the actor who acted, dates the message at the event time, and fills
// the body with the actual content (comment or issue/PR body + state). The
// content fetch is best-effort: on any error it falls back to the URL-only body
// (the prior behavior), so enrichment never breaks at-least-once delivery.
func (p *forgePoller) buildMessage(n forge.Notification) *mailbox.Message {
	url := n.Subject.HTMLURL
	if url == "" {
		url = n.Subject.URL
	}
	base := strings.ToLower(n.Subject.Type) // issue | pull | commit | repository
	kind := base
	from := mailbox.SourceForge
	body := ""

	// A comment event carries latest_comment_url; fetch the comment so the body
	// shows who said what.
	if cu := n.Subject.LatestCommentURL; cu != "" {
		if c, err := p.client.GetComment(cu); err == nil && c != nil && c.Body != "" {
			kind = base + "_comment"
			if c.User.Login != "" {
				from = c.User.Login
			}
			link := c.HTMLURL
			if link == "" {
				link = url
			}
			body = fmt.Sprintf("%s commented on %s [%s]:\n\n%s\n\n%s",
				from, url, n.Subject.State, c.Body, link)
		}
	}

	// Otherwise (or if the comment fetch failed) fetch the issue/PR itself.
	if body == "" {
		if sc, err := p.client.GetSubject(n.Subject.URL); err == nil && sc != nil {
			if sc.User.Login != "" {
				from = sc.User.Login
			}
			body = fmt.Sprintf("%s by %s [%s]:\n\n%s\n\n%s",
				capitalize(base), from, sc.State, strings.TrimSpace(sc.Body), url)
		}
	}

	if body == "" {
		body = url // last-resort fallback: bare URL, as before
	}

	return &mailbox.Message{
		Source:  mailbox.SourceForge,
		From:    from,
		Kind:    kind,
		Subject: forgeSubject(n, url, kind),
		Body:    body,
		Date:    parseForgeTime(n.Updated),
	}
}

// forgeSubject renders the §4 per-source Subject template for a forge
// notification: `<kind>: <repo>#<number> "<title>"`. The artifact ref is a fine
// priority cue; the URL/content stay in the body, never the Subject (proposal §4).
func forgeSubject(n forge.Notification, url, kind string) string {
	repo := n.Repository.FullName
	num := numberFromURL(url)
	if num > 0 {
		return fmt.Sprintf("%s: %s#%d %q", kind, repo, num, n.Subject.Title)
	}
	return fmt.Sprintf("%s: %s %q", kind, repo, n.Subject.Title)
}

// capitalize upper-cases the first byte of an ASCII word (issue -> Issue).
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parseForgeTime parses a forge RFC-3339 timestamp, returning the zero time on
// failure (Message.Encode then defaults Date to now).
func parseForgeTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
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
