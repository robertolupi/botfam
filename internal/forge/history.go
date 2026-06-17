package forge

import (
	"encoding/json"
	"fmt"
)

// History read endpoints used by `botfam mangle export`. These are read-only
// list/paginate calls over the existing authenticated Client (no new forge
// client, no extra SDK — see wiki concept-fragmentation).

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

const pageLimit = 50

// ListAllIssues returns every issue (state=all), excluding pull requests.
func (c *Client) ListAllIssues() ([]*Issue, error) {
	var out []*Issue
	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/%s/issues?type=issues&state=all&page=%d&limit=%d", c.Owner, c.Repo, page, pageLimit)
		b, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var batch []*Issue
		if err := json.Unmarshal(b, &batch); err != nil {
			return nil, fmt.Errorf("decode issues page %d: %w", page, err)
		}
		out = append(out, batch...)
		if len(batch) < pageLimit {
			return out, nil
		}
	}
}

// ListAllPulls returns every pull request (state=all).
func (c *Client) ListAllPulls() ([]*PullSummary, error) {
	var out []*PullSummary
	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls?state=all&page=%d&limit=%d", c.Owner, c.Repo, page, pageLimit)
		b, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var batch []*PullSummary
		if err := json.Unmarshal(b, &batch); err != nil {
			return nil, fmt.Errorf("decode pulls page %d: %w", page, err)
		}
		out = append(out, batch...)
		if len(batch) < pageLimit {
			return out, nil
		}
	}
}

// GetPullCommits returns the commits on a pull request (with author identity).
func (c *Client) GetPullCommits(num int) ([]*PullCommit, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/commits", c.Owner, c.Repo, num)
	b, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out []*PullCommit
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("decode pull %d commits: %w", num, err)
	}
	return out, nil
}
