package forge

import (
	"encoding/json"
	"fmt"
	"strings"
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

// SubjectContent is the fetched body of a notification's subject (issue or PR).
type SubjectContent struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// GetSubject fetches the content behind a notification's subject API URL so the
// caller can show it inline without a second round-trip. The subject URL is a
// full API URL; we re-base its path onto this client's BaseURL (and token) so it
// works regardless of the forge's configured ROOT_URL.
func (c *Client) GetSubject(apiURL string) (*SubjectContent, error) {
	const marker = "/api/v1/"
	i := strings.Index(apiURL, marker)
	if i < 0 {
		return nil, fmt.Errorf("unexpected subject url %q", apiURL)
	}
	b, err := c.request("GET", apiURL[i+len(marker):], nil)
	if err != nil {
		return nil, err
	}
	var sc SubjectContent
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("decode subject: %w", err)
	}
	return &sc, nil
}
