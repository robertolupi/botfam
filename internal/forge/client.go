package forge

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultHTTPClient is the shared fallback used by every Client that does not
// supply its own HTTPClient. Unlike http.DefaultClient it has a finite timeout
// so a slow or stalled Gitea host cannot wedge a caller (e.g. the forge-wait
// loop) forever. It is process-wide but never mutated, so it is safe to share.
var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

type Client struct {
	BaseURL string // e.g. "http://gitea:3000/"
	Owner   string // e.g. "botfam"
	Repo    string // e.g. "botfam"
	Token   string
	Remote  string

	// HTTPClient, when set, overrides defaultHTTPClient for this Client's
	// requests. Leave nil to use the timeout-bearing shared default.
	HTTPClient *http.Client
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
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	User struct {
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
		if hostPart == "gitea" {
			baseURL = "http://gitea:3000/"
		} else {
			baseURL = fmt.Sprintf("https://%s/", hostPart)
		}
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

		if hostPart == "gitea" {
			baseURL = "http://gitea:3000/"
		} else {
			baseURL = fmt.Sprintf("https://%s/", hostPart)
		}
		return baseURL, owner, repo, nil
	}

	return "", "", "", fmt.Errorf("unrecognized git remote URL format: %q", rawURL)
}

func NewClient(workDir string, actor string) (*Client, error) {
	baseURL := os.Getenv("GITEA_URL")
	owner := os.Getenv("GITEA_OWNER")
	repo := os.Getenv("GITEA_REPO")
	token := os.Getenv("GITEA_TOKEN")

	var famTOMLPath string
	if baseURL == "" || owner == "" || repo == "" {
		famTOMLPath = resolveFamTOMLPath(workDir)
		if famTOMLPath != "" {
			if baseURL == "" {
				baseURL = readConfigValueFromFamTOML(famTOMLPath, "forge_url", "forge-url")
			}
		}
	}

	remoteName := os.Getenv("BOTFAM_FORGE_REMOTE")
	if remoteName == "" && famTOMLPath != "" {
		remoteName = readConfigValueFromFamTOML(famTOMLPath, "forge_remote", "forge-remote")
	}
	if remoteName == "" {
		if famTOMLPath == "" {
			famTOMLPath = resolveFamTOMLPath(workDir)
		}
		if famTOMLPath != "" {
			remoteName = readConfigValueFromFamTOML(famTOMLPath, "forge_remote", "forge-remote")
		}
	}
	if remoteName == "" {
		remoteName = "gitea"
	}

	if baseURL == "" || owner == "" || repo == "" {
		cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.url", remoteName))
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
		return nil, errors.New("cannot resolve Gitea baseURL: remote URL could not be resolved and no forge_url is configured in fam.toml")
	}
	if owner == "" {
		return nil, errors.New("cannot resolve Gitea owner")
	}
	if repo == "" {
		return nil, errors.New("cannot resolve Gitea repo")
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

		fam := os.Getenv("BOTFAM_FAM")
		if fam == "" {
			absPath, err := filepath.Abs(workDir)
			if err == nil {
				fam = filepath.Base(filepath.Dir(absPath))
			}
		}
		if fam == "" {
			fam = "botfam"
		}

		tokenFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-%s-%s", fam, actor))
		if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
			legacyFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s", actor))
			if _, err := os.Stat(legacyFile); err == nil {
				tokenFile = legacyFile
			} else {
				testFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s-test", actor))
				if _, err := os.Stat(testFile); err == nil {
					tokenFile = testFile
				}
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
		Remote:  remoteName,
	}, nil
}

func (c *Client) Request(method, path string, body []byte) ([]byte, error) {
	return c.request(method, path, body)
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

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = defaultHTTPClient
	}
	resp, err := httpClient.Do(req)
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
		"event":     state,
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

func resolveFamTOMLPath(workDir string) string {
	if root := os.Getenv("COLLAB_ROOT"); root != "" {
		return filepath.Join(root, "fam.toml")
	}
	cmd := exec.Command("git", "rev-list", "--max-parents=0", "HEAD")
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	id := hex.EncodeToString(sum[:])[:12]
	name := "fam-" + id
	if suffix := os.Getenv("BOTFAM_FAM"); suffix != "" {
		var cleaned []rune
		for _, char := range suffix {
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
				cleaned = append(cleaned, char)
			}
		}
		name += "-" + string(cleaned)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".botfam", name, "fam.toml")
}

func readConfigValueFromFamTOML(path string, keys ...string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		for _, wantKey := range keys {
			if k == wantKey {
				v = strings.TrimSpace(v)
				if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
					return v[1 : len(v)-1]
				}
				if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
					return v[1 : len(v)-1]
				}
				return v
			}
		}
	}
	return ""
}

type Milestone struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

type Issue struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
	CreatedAt string `json:"created_at"`
	ClosedAt  string `json:"closed_at"`
}

type TimelineEvent struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	User      *struct {
		Login string `json:"login"`
	} `json:"user"`
	Body         string `json:"body"`
	RefCommitSHA string `json:"ref_commit_sha"`
	ReviewID     int64  `json:"review_id"`
}

func (c *Client) ListMilestones() ([]*Milestone, error) {
	var all []*Milestone
	page := 1
	limit := 50
	for {
		path := fmt.Sprintf("repos/%s/%s/milestones?state=all&page=%d&limit=%d", c.Owner, c.Repo, page, limit)
		b, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var list []*Milestone
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			break
		}
		all = append(all, list...)
		if len(list) < limit {
			break
		}
		page++
	}
	return all, nil
}

func (c *Client) ListIssuesByMilestone(milestoneID int64) ([]*Issue, error) {
	var all []*Issue
	page := 1
	limit := 50
	for {
		path := fmt.Sprintf("repos/%s/%s/issues?milestones=%d&state=all&type=all&page=%d&limit=%d", c.Owner, c.Repo, milestoneID, page, limit)
		b, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var list []*Issue
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			break
		}
		all = append(all, list...)
		if len(list) < limit {
			break
		}
		page++
	}
	return all, nil
}

func (c *Client) GetIssueTimeline(issueNum int) ([]*TimelineEvent, error) {
	var all []*TimelineEvent
	page := 1
	limit := 50
	for {
		path := fmt.Sprintf("repos/%s/%s/issues/%d/timeline?page=%d&limit=%d", c.Owner, c.Repo, issueNum, page, limit)
		b, err := c.request("GET", path, nil)
		if err != nil {
			return nil, err
		}
		var list []*TimelineEvent
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			break
		}
		all = append(all, list...)
		if len(list) < limit {
			break
		}
		page++
	}
	return all, nil
}
