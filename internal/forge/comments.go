package forge

import (
	"context"
	"fmt"
	"strings"

	giteasdk "gitea.dev/sdk"
)

// IssueComment is one discussion comment on an issue or PR.
type IssueComment struct {
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

// GetPRDiff returns the unified diff of a pull request.
func (c *Client) GetPRDiff(ctx context.Context, prNum int) (string, error) {
	b, _, err := c.sdk.PullRequests.GetPullRequestDiff(ctx, c.Owner, c.Repo, int64(prNum), giteasdk.PullRequestDiffOptions{Binary: false})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ListIssueComments returns the discussion comments on an issue or PR.
func (c *Client) ListIssueComments(ctx context.Context, num int) ([]IssueComment, error) {
	var all []IssueComment
	for page := 1; ; page++ {
		cs, resp, err := c.sdk.Issues.ListIssueComments(ctx, c.Owner, c.Repo, int64(num), giteasdk.ListIssueCommentOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, fmt.Errorf("list comments: %w", err)
		}
		for _, cmt := range cs {
			ic := IssueComment{Body: cmt.Body}
			if cmt.Poster != nil {
				ic.User.Login = cmt.Poster.UserName
			}
			all = append(all, ic)
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

// SubjectContent is the fetched body of a notification's subject (issue or PR).
type SubjectContent struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
}

// GetSubject fetches the content behind a notification's subject API URL.
// The URL is parsed to extract owner/repo/index and the right SDK method is
// called based on whether the subject is an issue or pull request.
func (c *Client) GetSubject(ctx context.Context, apiURL string) (*SubjectContent, error) {
	// Parse path segments from the API URL to extract resource type and index.
	const marker = "/api/v1/"
	i := strings.Index(apiURL, marker)
	if i < 0 {
		return nil, fmt.Errorf("unexpected subject url %q", apiURL)
	}
	path := strings.TrimPrefix(apiURL[i+len(marker):], "/")
	// path looks like: repos/{owner}/{repo}/issues/{index}
	//               or: repos/{owner}/{repo}/pulls/{index}
	parts := strings.Split(path, "/")
	if len(parts) < 5 || parts[0] != "repos" {
		return nil, fmt.Errorf("cannot parse subject url path %q", path)
	}
	owner, repo, kind := parts[1], parts[2], parts[3]
	var index int64
	if _, err := fmt.Sscanf(parts[4], "%d", &index); err != nil {
		return nil, fmt.Errorf("cannot parse index from subject url %q", apiURL)
	}

	sc := &SubjectContent{}
	switch kind {
	case "pulls":
		pr, _, err := c.sdk.PullRequests.GetPullRequest(ctx, owner, repo, index)
		if err != nil {
			return nil, fmt.Errorf("get subject PR %s/%s#%d: %w", owner, repo, index, err)
		}
		sc.Title = pr.Title
		sc.Body = pr.Body
		sc.State = string(pr.State)
		sc.HTMLURL = pr.HTMLURL
		if pr.Poster != nil {
			sc.User.Login = pr.Poster.UserName
		}
		for _, a := range pr.Assignees {
			sc.Assignees = append(sc.Assignees, struct {
				Login string `json:"login"`
			}{Login: a.UserName})
		}
	default: // "issues" or anything else
		iss, _, err := c.sdk.Issues.GetIssue(ctx, owner, repo, index)
		if err != nil {
			return nil, fmt.Errorf("get subject issue %s/%s#%d: %w", owner, repo, index, err)
		}
		sc.Title = iss.Title
		sc.Body = iss.Body
		sc.State = string(iss.State)
		sc.HTMLURL = iss.HTMLURL
		if iss.Poster != nil {
			sc.User.Login = iss.Poster.UserName
		}
		for _, a := range iss.Assignees {
			sc.Assignees = append(sc.Assignees, struct {
				Login string `json:"login"`
			}{Login: a.UserName})
		}
	}

	return sc, nil
}
