package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	giteasdk "gitea.dev/sdk"

	"github.com/robertolupi/botfam/internal/famconfig"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeForge builds a forge Client backed by an in-memory handler. The SDK
// accepts SetHTTPClient, so no TCP listener is bound — sandbox-safe (#73).
func fakeForge(handler http.HandlerFunc) *Client {
	hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Result(), nil
	})}
	sdk, _ := giteasdk.NewClient("http://forge.test",
		giteasdk.SetToken("test-token"),
		giteasdk.SetHTTPClient(hc),
		giteasdk.SetGiteaVersion(""),
	)
	return &Client{BaseURL: "http://forge.test/", Owner: "botfam", Repo: "botfam", Token: "test-token", sdk: sdk}
}

func TestClient_GetPR(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token test-token" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number": 1, "state": "open", "head": {"sha": "head-sha"}}`))
	})

	pr, err := client.GetPR(context.Background(), 1)
	if err != nil {
		t.Fatalf("failed to get PR: %v", err)
	}
	if pr.Index != 1 {
		t.Errorf("expected PR number 1, got %d", pr.Index)
	}
	if pr.Head == nil || pr.Head.Sha != "head-sha" {
		t.Errorf("expected HEAD SHA head-sha, got %v", pr.Head)
	}
}

func TestClient_GetPRReviews(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/reviews" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id": 123, "state": "APPROVED", "stale": false, "user": {"login": "agy-bot"}}]`))
	})

	reviews, err := client.GetPRReviews(context.Background(), 1)
	if err != nil {
		t.Fatalf("failed to get PR reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	if reviews[0].ID != 123 || string(reviews[0].State) != "APPROVED" || reviews[0].Reviewer == nil || reviews[0].Reviewer.UserName != "agy-bot" {
		t.Errorf("unexpected review data: %+v", reviews[0])
	}
}

func TestClient_PostPRReview(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/reviews" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PostPRReview(context.Background(), 1, "test-sha", "APPROVED", "looks good")
	if err != nil {
		t.Fatalf("failed to post PR review: %v", err)
	}
}

func TestClient_PostCommitStatus(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/botfam/botfam/statuses/test-sha" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	err := client.PostCommitStatus(context.Background(), "test-sha", "success", "test-context", "desc")
	if err != nil {
		t.Fatalf("failed to post commit status: %v", err)
	}
}

func TestParseGitRemoteURL(t *testing.T) {
	tests := []struct {
		url       string
		wantBase  string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			url:       "http://gitea:3000/botfam/botfam.git",
			wantBase:  "http://gitea:3000/",
			wantOwner: "botfam",
			wantRepo:  "botfam",
			wantErr:   false,
		},
		{
			url:       "git@github.com:robertolupi/botfam.git",
			wantBase:  "https://github.com/",
			wantOwner: "robertolupi",
			wantRepo:  "botfam",
			wantErr:   false,
		},
		{
			url:       "git@gitea:botfam/botfam.git",
			wantBase:  "http://gitea:3000/",
			wantOwner: "botfam",
			wantRepo:  "botfam",
			wantErr:   false,
		},
		{
			url:       "ssh://git@gitea/botfam/botfam.git",
			wantBase:  "http://gitea:3000/",
			wantOwner: "botfam",
			wantRepo:  "botfam",
			wantErr:   false,
		},
		{
			url:       "ssh://git@github.com/owner/repo.git",
			wantBase:  "https://github.com/",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			base, owner, repo, err := famconfig.ParseGitRemoteURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseGitRemoteURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if base != tt.wantBase {
				t.Errorf("base = %q, want %q", base, tt.wantBase)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestNewClient_Resolution(t *testing.T) {
	// Set up a temporary directory to act as a git worktree root.
	tempDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = eval
	}
	// The [repo.<k>] stanza is keyed by the parent (fam) dir so tempDir matches.
	famDir := filepath.Dir(tempDir)

	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_OWNER", "")
	t.Setenv("GITEA_REPO", "")
	t.Setenv("GITEA_TOKEN", "mock-token") // use env token to bypass token file reading
	t.Setenv("BOTFAM_FORGE_REMOTE", "")
	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))

	// Stanza with explicit forge_url + repository.
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{
			"test-fam": {Path: famDir, ForgeURL: "http://unified-forge:3000", Repository: "my-owner/my-repo"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// We need git-init in tempDir to make git rev-parse succeed
	runCmd := func(dir string, name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run %s %v: %v", name, args, err)
		}
	}
	runCmd(tempDir, "git", "init")

	// Call NewClient
	client, err := NewClientForWorkDir(tempDir, "agy")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	if client.BaseURL != "http://unified-forge:3000/" {
		t.Errorf("expected BaseURL http://unified-forge:3000/, got %s", client.BaseURL)
	}
	if client.Owner != "my-owner" {
		t.Errorf("expected Owner my-owner, got %s", client.Owner)
	}
	if client.Repo != "my-repo" {
		t.Errorf("expected Repo my-repo, got %s", client.Repo)
	}
	if client.Token != "mock-token" {
		t.Errorf("expected Token mock-token, got %s", client.Token)
	}

	// Fallback to git remote when the stanza has no forge_url/repository.
	if err := famconfig.WriteConfig(famconfig.Config{
		Repos: map[string]famconfig.RepoConfig{"test-fam": {Path: famDir}},
	}); err != nil {
		t.Fatal(err)
	}

	// Configure a mock remote
	runCmd(tempDir, "git", "remote", "add", "origin", "git@github.com:git-owner/git-repo.git")

	clientFallback, err := NewClientForWorkDir(tempDir, "agy")
	if err != nil {
		t.Fatalf("NewClient with fallback failed: %v", err)
	}

	if clientFallback.BaseURL != "https://github.com/" {
		t.Errorf("expected fallback BaseURL https://github.com/, got %s", clientFallback.BaseURL)
	}
	if clientFallback.Owner != "git-owner" {
		t.Errorf("expected fallback Owner git-owner, got %s", clientFallback.Owner)
	}
	if clientFallback.Repo != "git-repo" {
		t.Errorf("expected fallback Repo git-repo, got %s", clientFallback.Repo)
	}
}

// TestNewClient_AgentWorktreeViaResolveFam covers the new primary path (#231):
// a declared [agent.<name>] worktree resolves forge_url, repository, and the
// per-harness token in one shot through famconfig.ResolveFam.
func TestNewClient_AgentWorktreeViaResolveFam(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_OWNER", "")
	t.Setenv("GITEA_REPO", "")
	t.Setenv("GITEA_TOKEN", "")

	t.Setenv("BOTFAM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	famDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(famDir); err == nil {
		famDir = eval
	}
	if err := famconfig.WriteConfig(famconfig.Config{
		Agents: map[string]famconfig.AgentConfig{"claude": {Harness: "claude-code"}},
		Repos: map[string]famconfig.RepoConfig{
			"test-fam": {Path: famDir, ForgeURL: "http://agent-forge:3000", Repository: "agent-owner/agent-repo"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(famDir, "claude")
	if err := exec.Command("git", "init", wt).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Per-harness token at ~/.botfam/token-claude-code (HOME overridden above).
	botfamDir := filepath.Join(home, ".botfam")
	if err := os.MkdirAll(botfamDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(botfamDir, "token-claude-code"), []byte("agent-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := NewClientForWorkDir(wt, "claude")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.BaseURL != "http://agent-forge:3000/" {
		t.Errorf("BaseURL = %q, want http://agent-forge:3000/", client.BaseURL)
	}
	if client.Owner != "agent-owner" || client.Repo != "agent-repo" {
		t.Errorf("Owner/Repo = %q/%q, want agent-owner/agent-repo", client.Owner, client.Repo)
	}
	if client.Token != "agent-token" {
		t.Errorf("Token = %q, want agent-token (per-harness token via ResolveFam)", client.Token)
	}
}
