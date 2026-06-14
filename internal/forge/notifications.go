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

// notificationsPageLimit is the per-page size; maxNotificationPages caps total
// pagination so a runaway unread set cannot loop forever. maxNotificationPages
// is a var so tests can shrink it.
const notificationsPageLimit = 50

var maxNotificationPages = 100

// ListUnreadNotifications returns the first page of the agent's unread
// notification threads across all repositories — every subject type, not just
// pull requests. Sufficient for callers that only need "is anything unread?"
// (e.g. forge-wait). Callers that advance a high-water cursor must use
// ListAllUnreadNotifications instead, or they will skip notifications beyond
// page 1.
func (c *Client) ListUnreadNotifications() ([]Notification, error) {
	b, err := c.request("GET", fmt.Sprintf("notifications?status-types=unread&page=1&limit=%d", notificationsPageLimit), nil)
	if err != nil {
		return nil, err
	}
	var ns []Notification
	if err := json.Unmarshal(b, &ns); err != nil {
		return nil, fmt.Errorf("decode notifications: %w", err)
	}
	return ns, nil
}

// ListAllUnreadNotifications returns every unread notification thread, paging
// until a short page. A cursor-advancing consumer (the mailbox ingester) must
// enumerate the whole unread set before moving its high-water mark, otherwise an
// account with more than one page of unread can permanently skip same-repo
// notifications whose ids fall below an over-advanced cursor (#251 review).
func (c *Client) ListAllUnreadNotifications() ([]Notification, error) {
	var all []Notification
	for page := 1; page <= maxNotificationPages; page++ {
		b, err := c.request("GET", fmt.Sprintf("notifications?status-types=unread&page=%d&limit=%d", page, notificationsPageLimit), nil)
		if err != nil {
			return nil, err
		}
		var ns []Notification
		if err := json.Unmarshal(b, &ns); err != nil {
			return nil, fmt.Errorf("decode notifications: %w", err)
		}
		all = append(all, ns...)
		if len(ns) < notificationsPageLimit {
			return all, nil // a short page proves the unread set is fully enumerated
		}
	}
	// The page cap was exhausted while the last page was still full: the unread
	// set exceeds maxNotificationPages*limit and we have NOT enumerated all of it.
	// Returning the partial set would let a cursor advance past the unenumerated
	// tail (the #251 skip, at the cap boundary), so fail loudly instead.
	return nil, fmt.Errorf("forge: unread notifications exceed %d pages (>%d); refusing to report a partial unread set",
		maxNotificationPages, maxNotificationPages*notificationsPageLimit)
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
