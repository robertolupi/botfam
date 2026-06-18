package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

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
	if err != nil {
		return nil, err
	}
	return sdkPRToLocal(pr), nil
}

func (c *Client) GetPRReviews(ctx context.Context, prNum int) ([]*Review, error) {
	reviews, _, err := c.sdk.PullRequests.ListPullReviews(ctx, c.Owner, c.Repo, int64(prNum), giteasdk.ListPullReviewsOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]*Review, len(reviews))
	for i, r := range reviews {
		out[i] = &Review{
			ID:          r.ID,
			State:       string(r.State),
			Body:        r.Body,
			Stale:       r.Stale,
			SubmittedAt: r.Submitted.UTC().Format(time.RFC3339),
		}
		if r.Reviewer != nil {
			out[i].User.Login = r.Reviewer.UserName
		}
	}
	return out, nil
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
		for _, m := range ms {
			all = append(all, &Milestone{
				ID:    m.ID,
				Title: m.Title,
				State: string(m.State),
			})
		}
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
		for _, iss := range issues {
			all = append(all, sdkIssueToLocal(iss))
		}
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
		for _, ev := range evs {
			te := &TimelineEvent{
				ID:        ev.ID,
				Type:      ev.Type,
				CreatedAt: ev.Created.UTC().Format(time.RFC3339),
				Body:      ev.Body,
			}
			if ev.Poster != nil {
				te.User = &struct {
					Login string `json:"login"`
				}{Login: ev.Poster.UserName}
			}
			all = append(all, te)
		}
		if resp.NextPage == 0 {
			break
		}
	}
	return all, nil
}

// sdkPRToLocal converts an SDK PullRequest to our local type.
func sdkPRToLocal(pr *giteasdk.PullRequest) *PullRequest {
	local := &PullRequest{
		Number:    int(pr.Index),
		State:     string(pr.State),
		Merged:    pr.HasMerged,
		Mergeable: pr.Mergeable,
		Title:     pr.Title,
		Body:      pr.Body,
	}
	if pr.Poster != nil {
		local.User.Login = pr.Poster.UserName
	}
	if pr.Head != nil {
		local.Head.Ref = pr.Head.Ref
		local.Head.SHA = pr.Head.Sha
	}
	if pr.Base != nil {
		local.Base.Ref = pr.Base.Ref
		local.Base.SHA = pr.Base.Sha
	}
	return local
}

// sdkIssueToLocal converts an SDK Issue to our local type.
func sdkIssueToLocal(iss *giteasdk.Issue) *Issue {
	local := &Issue{
		ID:        iss.ID,
		Number:    int(iss.Index),
		Title:     iss.Title,
		Body:      iss.Body,
		State:     string(iss.State),
		CreatedAt: iss.Created.UTC().Format(time.RFC3339),
	}
	if iss.Poster != nil {
		local.User.Login = iss.Poster.UserName
	}
	if iss.Closed != nil {
		local.ClosedAt = iss.Closed.UTC().Format(time.RFC3339)
	}
	if iss.PullRequest != nil {
		local.PullRequest = &struct {
			URL string `json:"url"`
		}{URL: iss.HTMLURL}
	}
	for _, l := range iss.Labels {
		local.Labels = append(local.Labels, Label{ID: l.ID, Name: l.Name, Color: l.Color})
	}
	for _, a := range iss.Assignees {
		local.Assignees = append(local.Assignees, struct {
			Login string `json:"login"`
		}{Login: a.UserName})
	}
	if iss.Milestone != nil {
		local.Milestone = &struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		}{ID: iss.Milestone.ID, Title: iss.Milestone.Title}
	}
	return local
}

// WikiPage is a single wiki page from the forge.
type WikiPage struct {
	Title         string
	ContentBase64 string
	SubURL        string
	CommitSHA     string
	CommitDate    string // RFC3339
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
	wp := &WikiPage{
		Title:         p.Title,
		ContentBase64: p.ContentBase64,
		SubURL:        p.SubURL,
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
