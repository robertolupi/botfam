package forge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPRDiff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/pulls/7.diff" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("diff --git a/x b/x\n@@ -1 +1 @@\n-a\n+b\n"))
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL, Owner: "botfam", Repo: "botfam", Token: "t"}
	d, err := c.GetPRDiff(7)
	if err != nil {
		t.Fatalf("GetPRDiff: %v", err)
	}
	if want := "diff --git a/x b/x"; len(d) == 0 || d[:len(want)] != want {
		t.Errorf("unexpected diff: %q", d)
	}
}

func TestListIssueComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/7/comments" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"body":"looks good","user":{"login":"agy-bot"}}]`))
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL, Owner: "botfam", Repo: "botfam", Token: "t"}
	cs, err := c.ListIssueComments(7)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(cs) != 1 || cs[0].Body != "looks good" || cs[0].User.Login != "agy-bot" {
		t.Errorf("unexpected comments: %+v", cs)
	}
}
