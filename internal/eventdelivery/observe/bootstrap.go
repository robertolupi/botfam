package observe

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
)

// unreadBootstrapQuery is the source query recorded for bootstrap observations.
const unreadBootstrapQuery = "GET /notifications?status-types=unread"

// subjectNumberRe extracts the artifact number from a notification subject URL,
// e.g. ".../repos/botfam/botfam/issues/469" or ".../pulls/476".
var subjectNumberRe = regexp.MustCompile(`/(issues|pulls)/(\d+)`)

// Bootstrap implements the pre-session unread bootstrap rule: at session start
// (and on each poll) import ALL unread forge notification threads as raw
// observations, regardless of whether their updated_at precedes the session
// watermark. This is the backstop for ephemeral pokes (a mention or comment on
// an out-of-scope artifact) that have no standing worklist query and would
// otherwise be dropped by a watermark = now.
//
// The unread import is explicitly NOT watermark-gated — only the live push
// stream is. Each imported thread passes through translation at least once; the
// per-thread updated_at key lets a later poll mint a fresh observation when the
// thread sees new activity.
func (o *Observer) Bootstrap(ctx context.Context) ([]Observation, error) {
	threads, err := o.q.ListUnreadNotifications(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: list unread notifications: %w", err)
	}
	out := make([]Observation, 0, len(threads))
	for _, t := range threads {
		if !t.Unread {
			continue
		}
		kind := subjectKind(t.Subject.Type)
		number := subjectNumber(t)
		if kind == "" || number == 0 {
			// Repository/Commit-scoped or unparseable threads have no
			// issue/PR artifact identity; skip rather than mint a bad id.
			continue
		}
		out = append(out, Observation{
			Source:               Source,
			Repo:                 repoSlug(t),
			ArtifactKind:         kind,
			ArtifactNumber:       number,
			NotificationThreadID: strconv.FormatInt(t.ID, 10),
			EventKind:            EventNotification,
			EventKey:             notificationKey(t),
			EventClass:           ClassSyntheticID,
			SourceQuery:          unreadBootstrapQuery,
			PayloadJSON:          notificationPayload(t),
		})
	}
	return out, nil
}

func subjectKind(subjectType string) string {
	switch subjectType {
	case "Issue":
		return KindIssue
	case "Pull":
		return KindPull
	default:
		return ""
	}
}

func subjectNumber(t forge.Notification) int64 {
	for _, u := range []string{t.Subject.URL, t.Subject.HTMLURL} {
		if m := subjectNumberRe.FindStringSubmatch(u); m != nil {
			if n, err := strconv.ParseInt(m[2], 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func repoSlug(t forge.Notification) string {
	if t.Repository.FullName != "" {
		return t.Repository.FullName
	}
	return Source
}

// notificationKey prefers the thread's updated_at watermark so a thread with new
// activity yields a fresh observation; it falls back to the thread id when the
// timestamp is missing.
func notificationKey(t forge.Notification) string {
	if u := strings.TrimSpace(t.Updated); u != "" {
		return u
	}
	return "thread-" + strconv.FormatInt(t.ID, 10)
}

func notificationPayload(t forge.Notification) string {
	return mustJSON(artifactPayload{
		Number:    subjectNumber(t),
		Title:     t.Subject.Title,
		State:     t.Subject.State,
		HTMLURL:   t.Subject.HTMLURL,
		UpdatedAt: t.Updated,
	})
}
