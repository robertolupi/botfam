package forge

import (
	"context"
	"fmt"

	giteasdk "gitea.dev/sdk"
)

// Label is a repository label (the risk/*, triage/*, harness/* taxonomy lives
// here on the forge — see wiki proposal-process-risk-labels).
type Label = giteasdk.Label

// GetIssue returns one issue's metadata including its labels.
func (c *Client) GetIssue(ctx context.Context, num int) (*Issue, error) {
	iss, _, err := c.sdk.Issues.GetIssue(ctx, c.Owner, c.Repo, int64(num))
	if err != nil {
		return nil, fmt.Errorf("get issue %d: %w", num, err)
	}
	return iss, nil
}

// PostIssueComment posts a discussion comment on an issue or PR.
func (c *Client) PostIssueComment(ctx context.Context, num int, body string) error {
	_, _, err := c.sdk.Issues.CreateIssueComment(ctx, c.Owner, c.Repo, int64(num), giteasdk.CreateIssueCommentOption{Body: body})
	return err
}

// ListRepoLabels returns the repository's labels.
func (c *Client) ListRepoLabels(ctx context.Context) ([]Label, error) {
	var all []Label
	for page := 1; ; page++ {
		labels, resp, err := c.sdk.Issues.ListRepoLabels(ctx, c.Owner, c.Repo, giteasdk.ListLabelsOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, fmt.Errorf("list labels: %w", err)
		}
		for _, l := range labels {
			all = append(all, *l)
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

// AddLabels adds labels (by ID) to an issue or PR.
func (c *Client) AddLabels(ctx context.Context, num int, labelIDs []int64) error {
	if len(labelIDs) == 0 {
		return nil
	}
	_, _, err := c.sdk.Issues.AddIssueLabels(ctx, c.Owner, c.Repo, int64(num), giteasdk.IssueLabelsOption{Labels: labelIDs})
	return err
}
