package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestClient_GetIssue(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/306" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":306,"title":"x","body":"b","labels":[{"id":7,"name":"risk/superseded"}]}`))
	})
	iss, err := client.GetIssue(context.Background(), 306)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if iss.Index != 306 {
		t.Errorf("number: got %d", iss.Index)
	}
	if len(iss.Labels) != 1 || iss.Labels[0].Name != "risk/superseded" {
		t.Errorf("labels: got %+v", iss.Labels)
	}
}

func TestClient_PostIssueComment(t *testing.T) {
	var gotBody map[string]any
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/306/comments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	if err := client.PostIssueComment(context.Background(), 306, "hello"); err != nil {
		t.Fatalf("PostIssueComment: %v", err)
	}
	if gotBody["body"] != "hello" {
		t.Errorf("body: got %v", gotBody["body"])
	}
}

func TestClient_ListRepoLabels(t *testing.T) {
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"risk/phase-inversion"},{"id":2,"name":"triage/blocked"}]`))
	})
	labels, err := client.ListRepoLabels(context.Background())
	if err != nil {
		t.Fatalf("ListRepoLabels: %v", err)
	}
	if len(labels) != 2 || labels[0].Name != "risk/phase-inversion" {
		t.Errorf("labels: got %+v", labels)
	}
}

func TestClient_AddLabels(t *testing.T) {
	var gotBody map[string]any
	called := false
	client := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/306/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})
	if err := client.AddLabels(context.Background(), 306, []int64{1, 2}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	if ids, ok := gotBody["labels"].([]any); !ok || len(ids) != 2 {
		t.Errorf("labels payload: got %v", gotBody["labels"])
	}

	// Empty label set is a no-op that issues no request.
	called = false
	if err := client.AddLabels(context.Background(), 306, nil); err != nil {
		t.Fatalf("AddLabels(nil): %v", err)
	}
	if called {
		t.Errorf("AddLabels(nil) should not call the API")
	}
}
