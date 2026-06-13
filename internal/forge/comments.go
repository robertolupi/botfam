package forge

import (
	"encoding/json"
	"fmt"
)

// IssueComment is one discussion comment on an issue or PR.
type IssueComment struct {
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPRDiff returns the unified diff of a pull request.
func (c *Client) GetPRDiff(prNum int) (string, error) {
	b, err := c.request("GET", fmt.Sprintf("repos/%s/%s/pulls/%d.diff", c.Owner, c.Repo, prNum), nil)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ListIssueComments returns the discussion comments on an issue or PR (PRs and
// issues share the issue comment namespace).
func (c *Client) ListIssueComments(num int) ([]IssueComment, error) {
	b, err := c.request("GET", fmt.Sprintf("repos/%s/%s/issues/%d/comments", c.Owner, c.Repo, num), nil)
	if err != nil {
		return nil, err
	}
	var cs []IssueComment
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	return cs, nil
}
