package forge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_GetPR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token test-token" {
			t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number": 1, "state": "open", "head": {"sha": "head-sha"}}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL: server.URL,
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/reviews" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id": 123, "state": "APPROVED", "stale": false, "user": {"login": "agy-bot"}}]`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL: server.URL,
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer server.Close()

	client := &Client{
		BaseURL: server.URL,
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
	}

	err := client.PostPRReview(1, "test-sha", "APPROVED", "looks good")
	if err != nil {
		t.Fatalf("failed to post PR review: %v", err)
	}
}

func TestClient_PostCommitStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer server.Close()

	client := &Client{
		BaseURL: server.URL,
		Owner:   "botfam",
		Repo:    "botfam",
		Token:   "test-token",
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := &Client{
		BaseURL:    server.URL,
		Owner:      "botfam",
		Repo:       "botfam",
		Token:      "test-token",
		HTTPClient: &http.Client{Timeout: 20 * time.Millisecond},
	}

	_, err := client.GetPR(1)
	if err == nil {
		t.Fatal("expected a timeout error from a slow server, got nil")
	}
}
