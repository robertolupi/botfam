package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Client struct {
	BaseURL string // e.g. "http://gitea:3000/"
	Owner   string // e.g. "botfam"
	Repo    string // e.g. "botfam"
	Token   string
}

type PullRequest struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	Mergeable bool   `json:"mergeable"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Head      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base      struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type Review struct {
	ID          int64  `json:"id"`
	State       string `json:"state"` // e.g. "APPROVED", "REQUEST_CHANGES", "COMMENT"
	Body        string `json:"body"`
	Stale       bool   `json:"stale"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

func parseGitRemoteURL(rawURL string) (baseURL, owner, repo string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", "", errors.New("empty remote URL")
	}

	// Remove trailing .git
	rawURL = strings.TrimSuffix(rawURL, ".git")

	// Check if HTTP/HTTPS
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		parts := strings.Split(rawURL, "/")
		if len(parts) < 5 {
			return "", "", "", fmt.Errorf("invalid HTTP remote URL format: %q", rawURL)
		}
		repo = parts[len(parts)-1]
		owner = parts[len(parts)-2]
		baseURL = strings.Join(parts[:len(parts)-2], "/") + "/"
		return baseURL, owner, repo, nil
	}

	// Check if SSH format with ssh:// prefix
	if strings.HasPrefix(rawURL, "ssh://") {
		trimmed := strings.TrimPrefix(rawURL, "ssh://")
		slashIdx := strings.Index(trimmed, "/")
		if slashIdx == -1 {
			return "", "", "", fmt.Errorf("invalid ssh remote URL format: %q", rawURL)
		}
		pathPart := trimmed[slashIdx+1:]
		parts := strings.Split(pathPart, "/")
		if len(parts) != 2 {
			return "", "", "", fmt.Errorf("invalid ssh remote URL path format: %q", pathPart)
		}
		owner = parts[0]
		repo = parts[1]

		hostPart := trimmed[:slashIdx]
		if idx := strings.Index(hostPart, "@"); idx != -1 {
			hostPart = hostPart[idx+1:]
		}
		if idx := strings.Index(hostPart, ":"); idx != -1 {
			hostPart = hostPart[:idx]
		}
		baseURL = fmt.Sprintf("http://%s:3000/", hostPart)
		return baseURL, owner, repo, nil
	}

	// Check if SCP-like SSH format: git@gitea:botfam/botfam
	if strings.Contains(rawURL, ":") {
		parts := strings.SplitN(rawURL, ":", 2)
		hostPart := parts[0]
		pathPart := parts[1]

		if idx := strings.Index(hostPart, "@"); idx != -1 {
			hostPart = hostPart[idx+1:]
		}

		pathParts := strings.Split(pathPart, "/")
		if len(pathParts) != 2 {
			return "", "", "", fmt.Errorf("invalid SCP-like remote URL path: %q", pathPart)
		}
		owner = pathParts[0]
		repo = pathParts[1]

		baseURL = fmt.Sprintf("http://%s:3000/", hostPart)
		return baseURL, owner, repo, nil
	}

	return "", "", "", fmt.Errorf("unrecognized git remote URL format: %q", rawURL)
}

func NewClient(workDir string, actor string) (*Client, error) {
	baseURL := os.Getenv("GITEA_URL")
	owner := os.Getenv("GITEA_OWNER")
	repo := os.Getenv("GITEA_REPO")
	token := os.Getenv("GITEA_TOKEN")

	if baseURL == "" || owner == "" || repo == "" {
		cmd := exec.Command("git", "config", "--get", "remote.gitea.url")
		cmd.Dir = workDir
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err == nil {
			gBase, gOwner, gRepo, parseErr := parseGitRemoteURL(out.String())
			if parseErr == nil {
				if baseURL == "" {
					baseURL = gBase
				}
				if owner == "" {
					owner = gOwner
				}
				if repo == "" {
					repo = gRepo
				}
			}
		}
	}

	if baseURL == "" {
		baseURL = "http://gitea:3000/"
	}
	if owner == "" {
		owner = "botfam"
	}
	if repo == "" {
		repo = "botfam"
	}

	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	if token == "" {
		if actor == "" {
			return nil, errors.New("cannot resolve Gitea token: actor is empty")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home dir: %w", err)
		}

		tokenFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s", actor))
		if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
			testFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s-test", actor))
			if _, err := os.Stat(testFile); err == nil {
				tokenFile = testFile
			}
		}

		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read token file %s: %w", tokenFile, err)
		}
		token = strings.TrimSpace(string(b))
	}

	if token == "" {
		return nil, errors.New("access token is empty")
	}

	return &Client{
		BaseURL: baseURL,
		Owner:   owner,
		Repo:    repo,
		Token:   token,
	}, nil
}

func (c *Client) request(method, path string, body []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/%s", strings.TrimSuffix(c.BaseURL, "/"), strings.TrimPrefix(path, "/"))
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "token "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API request to %s failed with status %s: %s", url, resp.Status, string(respBytes))
	}

	return respBytes, nil
}

func (c *Client) GetPR(prNum int) (*PullRequest, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", c.Owner, c.Repo, prNum)
	b, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var pr PullRequest
	if err := json.Unmarshal(b, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (c *Client) GetPRReviews(prNum int) ([]*Review, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", c.Owner, c.Repo, prNum)
	b, err := c.request("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var reviews []*Review
	if err := json.Unmarshal(b, &reviews); err != nil {
		return nil, err
	}
	return reviews, nil
}

func (c *Client) PostPRReview(prNum int, commitSHA string, state string, body string) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", c.Owner, c.Repo, prNum)
	payload := map[string]any{
		"commit_id": commitSHA,
		"state":     state,
		"body":      body,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = c.request("POST", path, b)
	return err
}

func (c *Client) PostCommitStatus(commitSHA string, state string, context string, desc string) error {
	path := fmt.Sprintf("repos/%s/%s/statuses/%s", c.Owner, c.Repo, commitSHA)
	payload := map[string]any{
		"state":       state,
		"context":     context,
		"description": desc,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = c.request("POST", path, b)
	return err
}

func (c *Client) MergePR(prNum int, style string, msg string) error {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/merge", c.Owner, c.Repo, prNum)
	payload := map[string]any{
		"Do":                style,
		"MergeMessageField": msg,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = c.request("POST", path, b)
	return err
}
