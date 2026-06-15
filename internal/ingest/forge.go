package ingest

import (
	"fmt"
	"os"
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
	// ListUnreadRepoNotifications returns the newest page of unread threads scoped
	// to repo (owner/repo), most-recent first.
	ListUnreadRepoNotifications(repo string) ([]forge.Notification, error)
	// GetIssueTimeline returns the thread's typed event log (comment/close/
	// reopen/merge/review/…); its newest entry is what triggered the notification,
	// so the poller can name the actual event + actor without remembering prior
	// state. GetSubject fetches the issue/PR itself as a fallback. Both enrich the
	// spool body so `botfam wait` shows what happened — best-effort: the poller
	// falls back to a URL-only body on error.
	GetIssueTimeline(issueNum int) ([]*forge.TimelineEvent, error)
	GetSubject(apiURL string) (*forge.SubjectContent, error)
}

// forgePoller surfaces Gitea notifications as forge events using a **read-only
// id watermark** (#169): each poll lists the repo's unread threads and delivers
// only those with a notification id past the high-water mark, then advances the
// mark. It never marks notifications read — forge state stays canonical and
// unmutated (the spool, not the forge's read-flag, is this agent's record).
//
// On the first run for a spool it *seeds* — adopts the current high-water id and
// delivers nothing — so a fresh start (or the existing backlog you can see as
// "N unread" in the forge UI) doesn't flood the spool. ForgeSeeded (distinct
// from a 0 watermark) marks that baseline so the first real notification after a
// quiet start still delivers.
//
// Why this is safe vs the #251 review (which rejected an id-high-water cursor
// over an *offset*-paginated list): there is no offset here. Notification ids
// are stable and monotonic, so the watermark is a content identity, not a
// position that shifts as the list mutates — the skip hazard #251 worried about
// doesn't arise. A thread with new activity re-appears with a higher id and is
// re-delivered (the #253 updated-thread case still works). At-least-once holds:
// the watermark only advances after delivery, so a crash mid-poll re-surfaces
// undelivered threads next poll.
type forgePoller struct {
	client ForgeClient
	repo   string // owner/repo
	login  string // this agent's forge username (e.g. "claude-bot"); "" disables directed detection
}

// NewForgePoller builds the read-only watermark forge source for repo (owner/repo).
// login is the agent's forge username, used to mark events directed at it
// (assignee or @-mention); pass "" to skip directed detection.
func NewForgePoller(client ForgeClient, repo, login string) Poller {
	return &forgePoller{client: client, repo: repo, login: login}
}

func (p *forgePoller) Name() string { return mailbox.SourceForge }

func (p *forgePoller) Poll(s *mailbox.Spool, c *mailbox.Cursors) error {
	ns, err := p.client.ListUnreadRepoNotifications(p.repo)
	if err != nil {
		return err
	}
	maxID := c.ForgeWatermark
	for _, n := range ns {
		if n.ID > maxID {
			maxID = n.ID
		}
	}
	// Seed once: adopt the current high-water id and deliver nothing, so the
	// pre-existing backlog never floods the spool. Seeded is tracked separately
	// from the watermark so a seed that finds zero unread (watermark stays 0)
	// still delivers the first real notification later.
	if !c.ForgeSeeded {
		c.ForgeWatermark = maxID
		c.ForgeSeeded = true
		return nil
	}
	// Deliver only threads past the watermark, oldest-first so order is stable
	// and the mark advances monotonically. Advance only after a successful
	// deliver (at-least-once); a mid-poll error leaves the watermark for retry.
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
	for _, n := range ns {
		if n.ID <= c.ForgeWatermark {
			continue // already seen
		}
		if _, err := s.Deliver(p.buildMessage(n)); err != nil {
			return err
		}
		c.ForgeWatermark = n.ID
	}
	return nil
}

// buildMessage turns a forge notification into a spool message, enriched so
// `botfam wait` shows *what happened* — not just that something did. A single
// notification is a stateless snapshot (current state + latest comment), so it
// can't name the transition; the issue/PR *timeline* can. We read the newest
// timeline event (the one that triggered the notification) and name it: Kind
// `issue_closed` / `pull_merged` / `issue_comment` / `pull_review` …, From the
// actor who acted, Date the event time, and Body the event line (+ comment/
// review text). Best-effort: on any error or a non-issue/PR subject it falls
// back to GetSubject, then to a bare URL (the prior behavior), so enrichment
// never breaks at-least-once delivery. (Surfacing *every* event since last seen
// — not just the newest — needs a per-thread watermark; that's #169.)
func (p *forgePoller) buildMessage(n forge.Notification) *mailbox.Message {
	url := n.Subject.HTMLURL
	if url == "" {
		url = n.Subject.URL
	}
	base := strings.ToLower(n.Subject.Type) // issue | pull | commit | repository
	kind := base
	from := mailbox.SourceForge
	body := ""
	date := parseForgeTime(n.Updated)
	// Fail open: with no known login (auth unresolved) treat every event as
	// directed, so DND never silently drops real work.
	directed := p.login == ""

	// Fetch the issue/PR for its assignees (directed detection) and as the body
	// fallback. Best-effort: a nil subject just leaves directed=assignee unset.
	subj, _ := p.client.GetSubject(n.Subject.URL)
	if subj != nil && p.login != "" {
		for _, a := range subj.Assignees {
			if a.Login == p.login {
				directed = true // assigned to me
				break
			}
		}
	}

	if num := numberFromURL(url); num > 0 {
		if evs, err := p.client.GetIssueTimeline(int(num)); err == nil && len(evs) > 0 {
			ev := evs[len(evs)-1] // timeline is ascending; newest triggered this
			suffix, verb := timelineVerb(ev.Type)
			kind = base + "_" + suffix
			if ev.User != nil && ev.User.Login != "" {
				from = ev.User.Login
			}
			if t := parseForgeTime(ev.CreatedAt); !t.IsZero() {
				date = t
			}
			body = fmt.Sprintf("%s %s %s [%s]", from, verb, url, n.Subject.State)
			if txt := strings.TrimSpace(ev.Body); txt != "" {
				body += ":\n\n" + txt
				if p.login != "" && strings.Contains(txt, "@"+p.login) {
					directed = true // @-mentioned in the latest comment
				}
			}
			body += "\n\n" + url
		}
	}

	if body == "" && subj != nil { // timeline unavailable: fall back to the subject
		if subj.User.Login != "" {
			from = subj.User.Login
		}
		body = fmt.Sprintf("%s by %s [%s]:\n\n%s\n\n%s",
			capitalize(base), from, subj.State, strings.TrimSpace(subj.Body), url)
	}

	if body == "" {
		body = url // last-resort fallback: bare URL, as before
	}

	return &mailbox.Message{
		Source:   mailbox.SourceForge,
		From:     from,
		Kind:     kind,
		Subject:  forgeSubject(n, url, kind),
		Body:     body,
		Date:     date,
		Directed: directed,
	}
}

// timelineVerb maps a Gitea timeline event type to a (Kind suffix, human verb)
// so the message names the actual event. Unknown types pass through verbatim so
// a new Gitea event type degrades to something legible rather than vanishing.
func timelineVerb(t string) (kind, verb string) {
	switch t {
	case "comment":
		return "comment", "commented on"
	case "close":
		return "closed", "closed"
	case "reopen":
		return "reopened", "reopened"
	case "merge_pull":
		return "merged", "merged"
	case "review":
		return "review", "reviewed"
	case "review_request":
		return "review_requested", "requested review on"
	case "label", "unlabel":
		return "labeled", "updated labels on"
	case "assignees":
		return "assigned", "updated assignees on"
	case "pull_push":
		return "pushed", "pushed to"
	default:
		return t, t + " on"
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
	login, err := client.AuthLogin()
	if err != nil {
		// Can't resolve our forge username: directed detection fails open (every
		// event is treated as directed) so DND never silently drops real work.
		fmt.Fprintf(os.Stderr, "botfam: forge auth login unresolved (%v); directed detection disabled\n", err)
		login = ""
	}
	return NewForgePoller(client, rf.Repository, login), nil
}
