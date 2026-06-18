package forge

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	giteasdk "gitea.dev/sdk"

	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/robertolupi/botfam/internal/famctx"
)

// Client wraps the Gitea SDK client with fam-specific identity resolution.
// The SDK handles auth, pagination, and HTTP; this type adds fam-level
// Owner/Repo scoping and the botfam token-resolution path.
type Client struct {
	BaseURL string // e.g. "http://gitea:3000/"
	Owner   string // e.g. "botfam"
	Repo    string // e.g. "botfam"
	Token   string
	Remote  string

	sdk *giteasdk.Client
}

type PullRequest = giteasdk.PullRequest
type Review = giteasdk.PullReview
type Issue = giteasdk.Issue
type TimelineEvent = giteasdk.TimelineComment
type Milestone = giteasdk.Milestone
type User = giteasdk.User
type PullRequestMeta = giteasdk.PullRequestMeta
type StateType = giteasdk.StateType

// NewClient builds a Client from a context.Context enriched by famctx.WithFamCtx.
// It returns an error if ctx does not carry a famctx.Context (i.e. was not
// enriched at the dispatch boundary). Env overrides (GITEA_URL, GITEA_OWNER,
// GITEA_REPO, GITEA_TOKEN) win over the resolved values; the token is read from
// fctx.TokenPath, already keyed on the effective harness (#371).
func NewClient(ctx context.Context) (*Client, error) {
	fctx, ok := famctx.FromContext(ctx)
	if !ok {
		return nil, errors.New("forge: context does not carry a famctx (call famctx.WithFamCtx before reaching this point)")
	}
	baseURL := os.Getenv("GITEA_URL")
	owner := os.Getenv("GITEA_OWNER")
	repo := os.Getenv("GITEA_REPO")
	token := os.Getenv("GITEA_TOKEN")

	if token == "" && fctx.TokenPath != "" {
		if b, err := os.ReadFile(fctx.TokenPath); err == nil {
			token = strings.TrimSpace(string(b))
		}
	}
	if baseURL == "" {
		baseURL = fctx.Registry.ForgeURL
	}
	if owner == "" || repo == "" {
		if o, r, ok := famconfig.SplitOwnerRepo(fctx.Registry.Repository); ok {
			if owner == "" {
				owner = o
			}
			if repo == "" {
				repo = r
			}
		}
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if baseURL == "/" || baseURL == "" {
		return nil, errors.New("forge: no ForgeURL in resolved config; check ~/.botfam/config.toml")
	}
	if owner == "" {
		return nil, errors.New("forge: cannot resolve owner from resolved config")
	}
	if repo == "" {
		return nil, errors.New("forge: cannot resolve repo from resolved config")
	}
	if token == "" {
		return nil, fmt.Errorf("forge: token is empty (TokenPath=%q); run `botfam mint`", fctx.TokenPath)
	}
	return &Client{BaseURL: baseURL, Owner: owner, Repo: repo, Token: token}, nil
}

// NewClientForWorkDir builds a Client by re-resolving fam identity from workDir
// and actor. Use this only in contexts that do not have an enriched
// context.Context (e.g. MCP discovery, ingest goroutines). Prefer NewClient
// everywhere a context.Context is available.
func NewClientForWorkDir(workDir string, actor string) (*Client, error) {
	baseURL := os.Getenv("GITEA_URL")
	owner := os.Getenv("GITEA_OWNER")
	repo := os.Getenv("GITEA_REPO")
	token := os.Getenv("GITEA_TOKEN")

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
		harness := famconfig.ResolveHarness(declared, "", nil).Effective

		var tokenFile string
		if harness != "" {
			tokenFile, err = HarnessTokenPath(harness)
			if err != nil {
				return nil, err
			}
		}

		needTest := tokenFile == ""
		if !needTest {
			if _, statErr := os.Stat(tokenFile); os.IsNotExist(statErr) {
				needTest = true
			}
		}
		if needTest && os.Getenv("BOTFAM_ALLOW_TEST_TOKEN_FALLBACK") == "1" {
			testFile := fmt.Sprintf("%s/.botfam/token-botfam-%s-test", home, actor)
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

	sdkClient, err := giteasdk.NewClient(strings.TrimSuffix(baseURL, "/"),
		giteasdk.SetToken(token),
		giteasdk.SetGiteaVersion(""),
	)
	if err != nil {
		return nil, fmt.Errorf("create SDK client: %w", err)
	}

	return &Client{
		BaseURL: baseURL,
		Owner:   owner,
		Repo:    repo,
		Token:   token,
		Remote:  remoteName,
		sdk:     sdkClient,
	}, nil
}

// AuthLogin returns the login of the token's owner — e.g. "claude-bot" — so
// callers can recognise when an issue/PR is assigned to or mentions this agent.
func (c *Client) AuthLogin(ctx context.Context) (string, error) {
	u, _, err := c.sdk.Users.GetMyUserInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("get auth user: %w", err)
	}
	return u.UserName, nil
}

func (c *Client) GetPR(ctx context.Context, prNum int) (*PullRequest, error) {
	pr, _, err := c.sdk.PullRequests.GetPullRequest(ctx, c.Owner, c.Repo, int64(prNum))
	return pr, err
}

func (c *Client) GetPRReviews(ctx context.Context, prNum int) ([]*Review, error) {
	reviews, _, err := c.sdk.PullRequests.ListPullReviews(ctx, c.Owner, c.Repo, int64(prNum), giteasdk.ListPullReviewsOptions{})
	return reviews, err
}

func (c *Client) PostPRReview(ctx context.Context, prNum int, commitSHA string, state string, body string) error {
	_, _, err := c.sdk.PullRequests.CreatePullReview(ctx, c.Owner, c.Repo, int64(prNum), giteasdk.CreatePullReviewOptions{
		CommitID: commitSHA,
		State:    giteasdk.ReviewStateType(state),
		Body:     body,
	})
	return err
}

func (c *Client) PostCommitStatus(ctx context.Context, commitSHA string, state string, statusContext string, desc string) error {
	_, _, err := c.sdk.Repositories.CreateStatus(ctx, c.Owner, c.Repo, commitSHA, giteasdk.CreateStatusOption{
		State:       giteasdk.StatusState(state),
		Context:     statusContext,
		Description: desc,
	})
	return err
}

func (c *Client) ListMilestones(ctx context.Context) ([]*Milestone, error) {
	var all []*Milestone
	for page := 1; ; page++ {
		ms, resp, err := c.sdk.Issues.ListRepoMilestones(ctx, c.Owner, c.Repo, giteasdk.ListMilestoneOption{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
			State:       giteasdk.StateAll,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, ms...)
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

func (c *Client) ListIssuesByMilestone(ctx context.Context, milestoneID int64) ([]*Issue, error) {
	// Gitea's milestone filter uses the milestone title, but we have the ID.
	// Fetch milestone title first so we can filter by title.
	ms, _, err := c.sdk.Issues.GetMilestone(ctx, c.Owner, c.Repo, milestoneID)
	if err != nil {
		return nil, fmt.Errorf("get milestone %d: %w", milestoneID, err)
	}
	var all []*Issue
	for page := 1; ; page++ {
		issues, resp, err := c.sdk.Issues.ListRepoIssues(ctx, c.Owner, c.Repo, giteasdk.ListIssueOption{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
			Type:        giteasdk.IssueTypeIssue,
			State:       giteasdk.StateAll,
			Milestones:  []string{ms.Title},
		})
		if err != nil {
			return nil, err
		}
		all = append(all, issues...)
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

func (c *Client) GetIssueTimeline(ctx context.Context, issueNum int) ([]*TimelineEvent, error) {
	var all []*TimelineEvent
	for page := 1; ; page++ {
		evs, resp, err := c.sdk.Issues.ListIssueTimeline(ctx, c.Owner, c.Repo, int64(issueNum), giteasdk.ListIssueCommentOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, err
		}
		all = append(all, evs...)
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

// WikiPage is a single wiki page from the forge. It embeds the SDK type and
// adds Content (decoded ContentBase64) plus CommitSHA/CommitDate flattened from LastCommit.
type WikiPage struct {
	giteasdk.WikiPage
	Content    string // decoded from ContentBase64
	CommitSHA  string
	CommitDate string // RFC3339
}

// WikiPageMeta is wiki index metadata (no content).
type WikiPageMeta struct {
	Title      string
	SubURL     string
	CommitSHA  string
	CommitDate string
}

// GetWikiPage fetches a single wiki page by name.
func (c *Client) GetWikiPage(ctx context.Context, name string) (*WikiPage, error) {
	p, _, err := c.sdk.Wiki.GetPage(ctx, c.Owner, c.Repo, name)
	if err != nil {
		return nil, err
	}
	wp := &WikiPage{WikiPage: *p}
	if decoded, decErr := base64.StdEncoding.DecodeString(p.ContentBase64); decErr == nil {
		wp.Content = string(decoded)
	}
	if p.LastCommit != nil {
		wp.CommitSHA = p.LastCommit.ID
		if p.LastCommit.Author != nil {
			wp.CommitDate = p.LastCommit.Author.Date
		}
	}
	return wp, nil
}

// ListWikiPages returns wiki page metadata for all pages.
func (c *Client) ListWikiPages(ctx context.Context) ([]*WikiPageMeta, error) {
	var all []*WikiPageMeta
	for page := 1; ; page++ {
		pages, resp, err := c.sdk.Wiki.ListPages(ctx, c.Owner, c.Repo, giteasdk.ListWikiPagesOptions{
			ListOptions: giteasdk.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, err
		}
		for _, mp := range pages {
			m := &WikiPageMeta{
				Title:  mp.Title,
				SubURL: mp.SubURL,
			}
			if mp.LastCommit != nil {
				m.CommitSHA = mp.LastCommit.ID
				if mp.LastCommit.Author != nil {
					m.CommitDate = mp.LastCommit.Author.Date
				}
			}
			all = append(all, m)
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}
