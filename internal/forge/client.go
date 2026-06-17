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
	"time"

	"github.com/robertolupi/botfam/internal/famconfig"
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

func NewClient(workDir string, actor string) (*Client, error) {
	baseURL := os.Getenv("GITEA_URL")
	owner := os.Getenv("GITEA_OWNER")
	repo := os.Getenv("GITEA_REPO")
	token := os.Getenv("GITEA_TOKEN")

	// Resolve fam identity through the single canonical resolver (#231, #404). A
	// declared [agent.<name>] worktree yields forge_url, repository, and the
	// per-harness token path in one shot (env vars set above still win).
	// ResolveFam fails closed outside an agent worktree, so for non-agent /
	// base checkouts (and tools) we fall back to ResolveConfig (the merged
	// registry for the matching [repo.<k>] stanza), then to the git remote below.
	var reg famconfig.Registry
	var haveReg bool
	if rf, err := famconfig.ResolveFam(workDir); err == nil {
		reg, haveReg = rf.Registry, true
		if token == "" && rf.TokenPath != "" {
			if b, rerr := os.ReadFile(rf.TokenPath); rerr == nil {
				token = strings.TrimSpace(string(b))
			}
		}
	} else if r, rerr := famconfig.ResolveConfig(workDir); rerr == nil {
		reg, haveReg = r, true
	}
	if haveReg {
		if baseURL == "" {
			baseURL = reg.ForgeURL
		}
		if owner == "" || repo == "" {
			if o, r, ok := famconfig.SplitOwnerRepo(reg.Repository); ok {
				if owner == "" {
					owner = o
				}
				if repo == "" {
					repo = r
				}
			}
		}
	}

	// remoteName is the git remote consulted as a last-resort fallback below.
	// Overridable via BOTFAM_FORGE_REMOTE; defaults to "gitea". (The old
	// undocumented fam.toml forge_remote key is dropped — it was set nowhere.)
	remoteName := os.Getenv("BOTFAM_FORGE_REMOTE")
	if remoteName == "" {
		remoteName = "gitea"
	}

	if baseURL == "" || owner == "" || repo == "" {
		for _, rName := range []string{remoteName, "gitea", "origin"} {
			if rName == "" {
				continue
			}
			cmd := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.url", rName))
			cmd.Dir = workDir
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err == nil {
				gBase, gOwner, gRepo, parseErr := famconfig.ParseGitRemoteURL(out.String())
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
					break
				}
			}
		}
	}

	if baseURL == "" {
		return nil, errors.New("cannot resolve Gitea baseURL: remote URL could not be resolved and no forge_url is configured in ~/.botfam/config.toml")
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

		var declared string
		if haveReg {
			if a, ok := reg.Agents[actor]; ok {
				declared = a.Harness
			}
		}
		// Key the token on the harness actually running, not just whatever the
		// fam.toml declared: detect from the inherited env (botfam runs as a child
		// of the harness), falling back to the declared value (#371). clientInfo
		// isn't available on this CLI/forge path, so detection here is env-only.
		harness := famconfig.ResolveHarness(declared, "", nil).Effective

		// Fail closed: the token is the canonical per-harness one. There is NO
		// silent legacy (token-botfam-<actor>) or per-fam (token-<fam>-<actor>)
		// fallback — those masking a missing token are exactly the #183 disease.
		var tokenFile string
		if harness != "" {
			tokenFile, err = HarnessTokenPath(harness)
			if err != nil {
				return nil, err
			}
		}

		// The only non-canonical path is the explicit opt-in test credential
		// (BOTFAM_ALLOW_TEST_TOKEN_FALLBACK=1) for the local test forge — never a
		// silent production fallback (#70).
		needTest := tokenFile == ""
		if !needTest {
			if _, statErr := os.Stat(tokenFile); os.IsNotExist(statErr) {
				needTest = true
			}
		}
		if needTest && os.Getenv("BOTFAM_ALLOW_TEST_TOKEN_FALLBACK") == "1" {
			testFile := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s-test", actor))
			if _, statErr := os.Stat(testFile); statErr == nil {
				tokenFile = testFile
			}
		}

		if tokenFile == "" {
			return nil, fmt.Errorf("cannot resolve forge token: no [agent.%s] harness in ~/.botfam/config.toml — run `botfam setup`; report this to your operator (no legacy fallback)", actor)
		}
		b, err := os.ReadFile(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("forge token not found at %s: %w — mint it with `botfam mint --harness %s --user <forge-user>`; report this to your operator", tokenFile, err, harness)
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

// AuthLogin returns the login of the token's owner (GET /user) — e.g. "claude-bot"
// — so callers can recognize when an issue/PR is assigned to, or mentions, this
// agent. The actor name ("claude") is not the forge username, so it must be
// resolved from the forge, not assumed.
func (c *Client) AuthLogin() (string, error) {
	b, err := c.request("GET", "user", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return "", fmt.Errorf("decode user: %w", err)
	}
	return u.Login, nil
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
	Labels    []Label `json:"labels"`
	CreatedAt string  `json:"created_at"`
	ClosedAt  string  `json:"closed_at"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Milestone *struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	} `json:"milestone"`
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
