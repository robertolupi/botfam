package forge

import (
	"encoding/json"
	"fmt"
)

// Notification is one thread from the forge's per-user notifications feed
// (review requested, comment, mention, or an issue/PR assigned to the user).
type Notification struct {
	ID      int64 `json:"id"`
	Unread  bool  `json:"unread"`
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

// ListUnreadNotifications returns the agent's unread notification threads across
// all repositories — every subject type, not just pull requests.
func (c *Client) ListUnreadNotifications() ([]Notification, error) {
	b, err := c.request("GET", "notifications?status-types=unread&page=1&limit=50", nil)
	if err != nil {
		return nil, err
	}
	var ns []Notification
	if err := json.Unmarshal(b, &ns); err != nil {
		return nil, fmt.Errorf("decode notifications: %w", err)
	}
	return ns, nil
}

// MarkNotificationRead marks a single notification thread read so it does not
// wake the agent again. Requires the write:notification token scope.
func (c *Client) MarkNotificationRead(id int64) error {
	_, err := c.request("PATCH", fmt.Sprintf("notifications/threads/%d?to-status=read", id), nil)
	return err
}
