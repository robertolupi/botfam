package forge

import (
	"context"
	"fmt"
	"time"

	giteasdk "gitea.dev/sdk"
)

// History read endpoints used by `botfam mangle export`. These are read-only
// list/paginate calls over the SDK-backed Client.

// PullSummary is the subset of a pull request returned by the list endpoint
// that the fact exporter needs (the timestamps the issue list omits).
type PullSummary struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	CreatedAt string `json:"created_at"`
	MergedAt  string `json:"merged_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

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
	out := make([]*Issue, len(issues))
	for i, iss := range issues {
		out[i] = sdkIssueToLocal(iss)
	}
	return out, nil
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
		for _, iss := range issues {
			out = append(out, sdkIssueToLocal(iss))
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return out, nil
}

// ListAllPulls returns every pull request (state=all).
func (c *Client) ListAllPulls(ctx context.Context) ([]*PullSummary, error) {
	var out []*PullSummary
	for page := 1; ; page++ {
		pulls, resp, err := c.sdk.PullRequests.ListRepoPullRequests(ctx, c.Owner, c.Repo, giteasdk.ListPullRequestsOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: sdkPageLimit},
			State:       giteasdk.StateAll,
		})
		if err != nil {
			return nil, fmt.Errorf("list pulls page %d: %w", page, err)
		}
		for _, pr := range pulls {
			ps := &PullSummary{
				Number: int(pr.Index),
				Title:  pr.Title,
				Body:   pr.Body,
				State:  string(pr.State),
				Merged: pr.HasMerged,
			}
			if pr.Created != nil {
				ps.CreatedAt = pr.Created.UTC().Format(time.RFC3339)
			}
			if pr.Merged != nil {
				ps.MergedAt = pr.Merged.UTC().Format(time.RFC3339)
			}
			if pr.Poster != nil {
				ps.User.Login = pr.Poster.UserName
			}
			out = append(out, ps)
		}
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
