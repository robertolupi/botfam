package forge

import (
	"encoding/json"
	"fmt"
)

// Label is a repository label (the risk/*, triage/*, harness/* taxonomy lives
// here on the forge — see wiki proposal-process-risk-labels).
type Label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// GetIssue returns one issue's metadata including its labels. PRs share the
// issue namespace, so this also resolves a PR's labels when given a PR index.
func (c *Client) GetIssue(num int) (*Issue, error) {
	b, err := c.request("GET", fmt.Sprintf("repos/%s/%s/issues/%d", c.Owner, c.Repo, num), nil)
	if err != nil {
		return nil, err
	}
	var iss Issue
	if err := json.Unmarshal(b, &iss); err != nil {
		return nil, fmt.Errorf("decode issue %d: %w", num, err)
	}
	return &iss, nil
}

// PostIssueComment posts a discussion comment on an issue or PR (they share the
// issue comment namespace).
func (c *Client) PostIssueComment(num int, body string) error {
	payload, err := json.Marshal(map[string]any{"body": body})
	if err != nil {
		return err
	}
	_, err = c.request("POST", fmt.Sprintf("repos/%s/%s/issues/%d/comments", c.Owner, c.Repo, num), payload)
	return err
}

// ListRepoLabels returns the repository's labels. The forge paginates; this
// walks pages until a short page is returned (the label set is small).
func (c *Client) ListRepoLabels() ([]Label, error) {
	var all []Label
	for page := 1; ; page++ {
		b, err := c.request("GET", fmt.Sprintf("repos/%s/%s/labels?page=%d&limit=50", c.Owner, c.Repo, page), nil)
		if err != nil {
			return nil, err
		}
		var batch []Label
		if err := json.Unmarshal(b, &batch); err != nil {
			return nil, fmt.Errorf("decode labels: %w", err)
		}
		all = append(all, batch...)
		if len(batch) < 50 {
			return all, nil
		}
	}
}

// AddLabels adds labels (by ID) to an issue or PR. Gitea/Forgejo's add-labels
// endpoint is additive, so existing labels are preserved.
func (c *Client) AddLabels(num int, labelIDs []int64) error {
	if len(labelIDs) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"labels": labelIDs})
	if err != nil {
		return err
	}
	_, err = c.request("POST", fmt.Sprintf("repos/%s/%s/issues/%d/labels", c.Owner, c.Repo, num), payload)
	return err
}
