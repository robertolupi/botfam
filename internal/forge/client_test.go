package forge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
		if payload["commit_id"] != "test-sha" || payload["state"] != "APPROVED" || payload["body"] != "looks good" {
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

func TestClient_MergePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/1/merge" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		if payload["Do"] != "merge" || payload["MergeMessageField"] != "msg" {
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

	err := client.MergePR(1, "merge", "msg")
	if err != nil {
		t.Fatalf("failed to merge PR: %v", err)
	}
}
