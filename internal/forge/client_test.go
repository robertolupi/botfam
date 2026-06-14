package forge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeClient returns an *http.Client whose transport serves handler against an
// in-memory httptest.ResponseRecorder. Unlike httptest.NewServer it binds no TCP
// listener, so forge unit tests run in sandboxes that deny local bind (#73).
func fakeClient(handler http.HandlerFunc) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec.Result(), nil
	})}
}

func TestClient_GetPR(t *testing.T) {
	client := &Client{
		BaseURL: "http://forge.test",
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "token test-token" {
				t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"number": 1, "state": "open", "head": {"sha": "head-sha"}}`))
		}),
	}

	pr, err := client.GetPR(1)
	if err != nil {
		t.Fatalf("failed to get PR: %v", err)
	}
	if pr.Number != 1 {
		t.Errorf("expected PR number 1, got %d", pr.Number)
	}
	if pr.Head.SHA != "head-sha" {
		t.Errorf("expected HEAD SHA head-sha, got %s", pr.Head.SHA)
	}
}

func TestClient_GetPRReviews(t *testing.T) {
	client := &Client{
		BaseURL: "http://forge.test",
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/reviews" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id": 123, "state": "APPROVED", "stale": false, "user": {"login": "agy-bot"}}]`))
		}),
	}

	reviews, err := client.GetPRReviews(1)
	if err != nil {
		t.Fatalf("failed to get PR reviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("expected 1 review, got %d", len(reviews))
	}
	if reviews[0].ID != 123 || reviews[0].State != "APPROVED" || reviews[0].User.Login != "agy-bot" {
		t.Errorf("unexpected review data: %+v", reviews[0])
	}
}

func TestClient_PostPRReview(t *testing.T) {
	client := &Client{
		BaseURL: "http://forge.test",
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/reviews" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("failed to decode body: %v", err)
			}
			if payload["commit_id"] != "test-sha" || payload["event"] != "APPROVED" || payload["body"] != "looks good" {
				t.Errorf("unexpected payload: %v", payload)
			}
			w.WriteHeader(http.StatusOK)
		}),
	}

	err := client.PostPRReview(1, "test-sha", "APPROVED", "looks good")
	if err != nil {
		t.Fatalf("failed to post PR review: %v", err)
	}
}

func TestClient_PostCommitStatus(t *testing.T) {
	client := &Client{
		BaseURL: "http://forge.test",
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/v1/repos/botfam/botfam/statuses/test-sha" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("failed to decode body: %v", err)
			}
			if payload["state"] != "success" || payload["context"] != "test-context" || payload["description"] != "desc" {
				t.Errorf("unexpected payload: %v", payload)
			}
			w.WriteHeader(http.StatusOK)
		}),
	}

	err := client.PostCommitStatus("test-sha", "success", "test-context", "desc")
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
			base, owner, repo, err := parseGitRemoteURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitRemoteURL() error = %v, wantErr %v", err, tt.wantErr)
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

func TestDefaultHTTPClientHasTimeout(t *testing.T) {
	// http.DefaultClient has Timeout 0 (wait forever); the package fallback
	// must not, so a stalled forge cannot wedge a caller indefinitely.
	if defaultHTTPClient.Timeout <= 0 {
		t.Fatalf("defaultHTTPClient must have a positive timeout, got %v", defaultHTTPClient.Timeout)
	}
}

func TestClient_RequestRespectsHTTPClientTimeout(t *testing.T) {
	// A transport that blocks until the request context is cancelled lets us
	// assert Client.Timeout fires — without binding a TCP listener.
	client := &Client{
		BaseURL: "http://forge.test",
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
		HTTPClient: &http.Client{
			Timeout: 20 * time.Millisecond,
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				<-req.Context().Done()
				return nil, req.Context().Err()
			}),
		},
	}

	_, err := client.GetPR(1)
	if err == nil {
		t.Fatal("expected a timeout error from a stalled transport, got nil")
	}
}

func TestNewClient_Resolution(t *testing.T) {
	// Set up a temporary directory to act as a git worktree root
	tempDir := t.TempDir()
	if eval, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = eval
	}

	// Create a mock fam.toml in the parent directory to simulate unified-fam-config
	// structure where famDir contains the agent worktree (tempDir)
	famDir := filepath.Dir(tempDir)
	famTOML := filepath.Join(famDir, "fam.toml")

	// Clean up any existing fam.toml in parent temp directory after test
	defer os.Remove(famTOML)

	// Setenv COLLAB_ROOT empty so it falls back to git/fam.toml resolution
	t.Setenv("COLLAB_ROOT", "")
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_OWNER", "")
	t.Setenv("GITEA_REPO", "")
	t.Setenv("GITEA_TOKEN", "mock-token") // use env token to bypass token file reading

	// Write unified fam.toml
	content := `name = "test-fam"
forge_url = "http://unified-forge:3000"
repository = "my-owner/my-repo"
`
	if err := os.WriteFile(famTOML, []byte(content), 0644); err != nil {
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
	client, err := NewClient(tempDir, "agy")
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

	// Test fallback to git remote when fam.toml doesn't have forge_url/repository
	contentNoForge := `name = "test-fam"`
	if err := os.WriteFile(famTOML, []byte(contentNoForge), 0644); err != nil {
		t.Fatal(err)
	}

	// Configure a mock remote
	runCmd(tempDir, "git", "remote", "add", "origin", "git@github.com:git-owner/git-repo.git")

	clientFallback, err := NewClient(tempDir, "agy")
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
