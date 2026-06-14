package forge

import (
	"net/http"
	"testing"
)

func TestListUnreadNotifications(t *testing.T) {
	c := &Client{
		BaseURL: "http://forge.test", Token: "t",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/notifications" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.URL.Query().Get("status-types") != "unread" {
				t.Errorf("expected status-types=unread, got %q", r.URL.RawQuery)
			}
			if r.Header.Get("Authorization") != "token t" {
				t.Errorf("unexpected auth: %s", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":5,"unread":true,"subject":{"title":"Look at this","type":"Issue","html_url":"http://h/botfam/botfam/issues/1"},"repository":{"full_name":"botfam/botfam"}}]`))
		}),
	}
	ns, err := c.ListUnreadNotifications()
	if err != nil {
		t.Fatalf("ListUnreadNotifications: %v", err)
	}
	if len(ns) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(ns))
	}
	if ns[0].ID != 5 || ns[0].Subject.Type != "Issue" || ns[0].Repository.FullName != "botfam/botfam" {
		t.Errorf("unexpected notification: %+v", ns[0])
	}
	if ns[0].Subject.HTMLURL != "http://h/botfam/botfam/issues/1" {
		t.Errorf("unexpected html_url: %s", ns[0].Subject.HTMLURL)
	}
}

func TestListUnreadRepoNotifications(t *testing.T) {
	c := &Client{
		BaseURL: "http://forge.test", Token: "t",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			// Must hit the repo-scoped endpoint so Gitea filters server-side.
			if r.URL.Path != "/api/v1/repos/botfam/botfam/notifications" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			if r.URL.Query().Get("status-types") != "unread" {
				t.Errorf("expected status-types=unread, got %q", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":7,"unread":true,"subject":{"title":"PR","type":"Pull","html_url":"http://h/botfam/botfam/pulls/9"},"repository":{"full_name":"botfam/botfam"}}]`))
		}),
	}
	ns, err := c.ListUnreadRepoNotifications("botfam/botfam")
	if err != nil {
		t.Fatalf("ListUnreadRepoNotifications: %v", err)
	}
	if len(ns) != 1 || ns[0].ID != 7 || ns[0].Repository.FullName != "botfam/botfam" {
		t.Errorf("unexpected notifications: %+v", ns)
	}
}

func TestGetSubject(t *testing.T) {
	c := &Client{
		BaseURL: "http://forge.test", Token: "t",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/11" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"title":"T","body":"hello body","state":"open","html_url":"http://h/x"}`))
		}),
	}
	// Subject URL carries a DIFFERENT host (mimics a ROOT_URL mismatch); GetSubject
	// must re-base the path onto c.BaseURL.
	sc, err := c.GetSubject("http://wrong-host:9999/api/v1/repos/botfam/botfam/issues/11")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if sc.Body != "hello body" || sc.State != "open" {
		t.Errorf("unexpected subject content: %+v", sc)
	}
}

func TestMarkNotificationRead(t *testing.T) {
	c := &Client{
		BaseURL: "http://forge.test", Token: "t",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PATCH" || r.URL.Path != "/api/v1/notifications/threads/5" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			if r.URL.Query().Get("to-status") != "read" {
				t.Errorf("expected to-status=read, got %q", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	if err := c.MarkNotificationRead(5); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
}
