package forge

import (
	"context"
	"fmt"

	giteasdk "gitea.dev/sdk"
)

// History read endpoints used by `botfam mangle export`. These are read-only
// list/paginate calls over the SDK-backed Client.

// PullCommit is one commit on a PR, with author identity and timestamp.
type PullCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

// AuthorLogin returns the forge login if known, else the git author name.
func (pc PullCommit) AuthorLogin() string {
	if pc.Author != nil && pc.Author.Login != "" {
		return pc.Author.Login
	}
	return pc.Commit.Author.Name
}

const sdkPageLimit = 50

// ListRecentIssues returns one page of issues (newest first), useful for
// hygiene checks that only need recent items without fetching the full history.
func (c *Client) ListRecentIssues(ctx context.Context, page, pageSize int) ([]*Issue, error) {
	issues, _, err := c.sdk.Issues.ListRepoIssues(ctx, c.Owner, c.Repo, giteasdk.ListIssueOption{
		ListOptions: giteasdk.ListOptions{Page: page, PageSize: pageSize},
		Type:        giteasdk.IssueTypeIssue,
		State:       giteasdk.StateAll,
	})
	if err != nil {
		return nil, fmt.Errorf("list recent issues: %w", err)
	}
	return issues, nil
}

// ListAllIssues returns every issue (excluding pull requests), state=all.
func (c *Client) ListAllIssues(ctx context.Context) ([]*Issue, error) {
	var out []*Issue
	for page := 1; ; page++ {
		issues, resp, err := c.sdk.Issues.ListRepoIssues(ctx, c.Owner, c.Repo, giteasdk.ListIssueOption{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: sdkPageLimit},
			Type:        giteasdk.IssueTypeIssue,
			State:       giteasdk.StateAll,
		})
		if err != nil {
			return nil, fmt.Errorf("list issues page %d: %w", page, err)
		}
		out = append(out, issues...)
		if resp.NextPage == 0 {
			break
		}
	}
	return out, nil
}

// ListAllPulls returns every pull request (state=all).
func (c *Client) ListAllPulls(ctx context.Context) ([]*PullRequest, error) {
	var out []*PullRequest
	for page := 1; ; page++ {
		pulls, resp, err := c.sdk.PullRequests.ListRepoPullRequests(ctx, c.Owner, c.Repo, giteasdk.ListPullRequestsOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: sdkPageLimit},
			State:       giteasdk.StateAll,
		})
		if err != nil {
			return nil, fmt.Errorf("list pulls page %d: %w", page, err)
		}
		out = append(out, pulls...)
		if resp.NextPage == 0 {
			break
		}
	}
	return out, nil
}

// GetPullCommits returns the commits on a pull request (with author identity).
func (c *Client) GetPullCommits(ctx context.Context, num int) ([]*PullCommit, error) {
	var out []*PullCommit
	for page := 1; ; page++ {
		commits, resp, err := c.sdk.PullRequests.ListPullRequestCommits(ctx, c.Owner, c.Repo, int64(num), giteasdk.ListPullRequestCommitsOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: sdkPageLimit},
		})
		if err != nil {
			return nil, fmt.Errorf("get pull %d commits page %d: %w", num, page, err)
		}
		for _, cm := range commits {
			pc := &PullCommit{SHA: cm.SHA}
			if cm.RepoCommit != nil && cm.RepoCommit.Author != nil {
				pc.Commit.Author.Name = cm.RepoCommit.Author.Name
				pc.Commit.Author.Date = cm.RepoCommit.Author.Date
			}
			if cm.Author != nil {
				pc.Author = &struct {
					Login string `json:"login"`
				}{Login: cm.Author.UserName}
			}
			out = append(out, pc)
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return out, nil
}
