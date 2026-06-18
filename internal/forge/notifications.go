package forge

import (
	"context"
	"fmt"
	"strings"
	"time"

	giteasdk "gitea.dev/sdk"
)

// Notification is one thread from the forge's per-user notifications feed.
type Notification struct {
	ID      int64  `json:"id"`
	Unread  bool   `json:"unread"`
	Updated string `json:"updated_at"`
	Subject struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		HTMLURL string `json:"html_url"`
		Type    string `json:"type"` // Issue | Pull | Commit | Repository
		State   string `json:"state"`
	} `json:"subject"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// notificationsPageLimit is the per-page size for notification fetches.
const notificationsPageLimit = 50

// ListUnreadNotifications returns the first page of the agent's unread
// notification threads across all repositories.
func (c *Client) ListUnreadNotifications(ctx context.Context) ([]Notification, error) {
	threads, _, err := c.sdk.Notifications.List(ctx, giteasdk.ListNotificationOptions{
		ListOptions: giteasdk.ListOptions{Page: 1, PageSize: notificationsPageLimit},
		Status:      []giteasdk.NotificationStatus{giteasdk.NotificationStatusUnread},
	})
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return sdkThreadsToLocal(threads), nil
}

// ListUnreadRepoNotifications returns one page of unread notification threads
// scoped to a single repo (owner/repo).
func (c *Client) ListUnreadRepoNotifications(ctx context.Context, repo string) ([]Notification, error) {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok {
		owner, repoName = c.Owner, repo
	}
	threads, _, err := c.sdk.Notifications.ListByRepo(ctx, owner, repoName, giteasdk.ListNotificationOptions{
		ListOptions: giteasdk.ListOptions{Page: 1, PageSize: notificationsPageLimit},
		Status:      []giteasdk.NotificationStatus{giteasdk.NotificationStatusUnread},
	})
	if err != nil {
		return nil, fmt.Errorf("list repo notifications: %w", err)
	}
	return sdkThreadsToLocal(threads), nil
}

// MarkNotificationRead marks a single notification thread read.
func (c *Client) MarkNotificationRead(ctx context.Context, id int64) error {
	_, _, err := c.sdk.Notifications.MarkReadByID(ctx, id, giteasdk.NotificationStatusRead)
	return err
}

func sdkThreadsToLocal(threads []*giteasdk.NotificationThread) []Notification {
	out := make([]Notification, len(threads))
	for i, t := range threads {
		n := Notification{
			ID:     t.ID,
			Unread: t.Unread,
		}
		if !t.UpdatedAt.IsZero() {
			n.Updated = t.UpdatedAt.UTC().Format(time.RFC3339)
		}
		if t.Subject != nil {
			n.Subject.Title = t.Subject.Title
			n.Subject.URL = t.Subject.URL
			n.Subject.HTMLURL = t.Subject.HTMLURL
			n.Subject.Type = string(t.Subject.Type)
			n.Subject.State = string(t.Subject.State)
		}
		if t.Repository != nil {
			n.Repository.FullName = t.Repository.FullName
		}
		out[i] = n
	}
	return out
}
