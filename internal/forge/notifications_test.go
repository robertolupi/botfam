package forge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListUnreadNotifications(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL, Token: "t"}
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

func TestMarkNotificationRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/v1/notifications/threads/5" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("to-status") != "read" {
			t.Errorf("expected to-status=read, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := &Client{BaseURL: server.URL, Token: "t"}
	if err := c.MarkNotificationRead(5); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
}
