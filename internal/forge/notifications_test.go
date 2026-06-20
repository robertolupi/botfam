package forge

import (
	"context"
	"net/http"
	"testing"
)

func TestListUnreadNotifications(t *testing.T) {
	c := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":5,"unread":true,"subject":{"title":"Look at this","type":"Issue","html_url":"http://h/botfam/botfam/issues/1"},"repository":{"full_name":"botfam/botfam","name":"botfam","owner":{"login":"botfam"}}}]`))
	})
	ns, err := c.ListUnreadNotifications(context.Background())
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

func TestListAllUnreadNotificationsPaginates(t *testing.T) {
	c := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "", "1":
			// Signal a second page via the Link header rel="next".
			w.Header().Set("Link", `</api/v1/notifications?page=2>; rel="next"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1,"unread":true,"subject":{"title":"p1","type":"Issue","html_url":"http://h/botfam/botfam/issues/1"},"repository":{"full_name":"botfam/botfam"}}]`))
		case "2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":2,"unread":true,"subject":{"title":"p2","type":"Pull","html_url":"http://h/botfam/botfam/pulls/2"},"repository":{"full_name":"botfam/botfam"}}]`))
		default:
			t.Errorf("unexpected page: %s", r.URL.Query().Get("page"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}
	})
	ns, err := c.ListAllUnreadNotifications(context.Background())
	if err != nil {
		t.Fatalf("ListAllUnreadNotifications: %v", err)
	}
	if len(ns) != 2 {
		t.Fatalf("expected 2 notifications across pages, got %d: %+v", len(ns), ns)
	}
	if ns[0].ID != 1 || ns[1].ID != 2 {
		t.Errorf("unexpected paginated order: %+v", ns)
	}
}

func TestListUnreadRepoNotifications(t *testing.T) {
	c := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/notifications" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":7,"unread":true,"subject":{"title":"PR","type":"Pull","html_url":"http://h/botfam/botfam/pulls/9"},"repository":{"full_name":"botfam/botfam","name":"botfam","owner":{"login":"botfam"}}}]`))
	})
	ns, err := c.ListUnreadRepoNotifications(context.Background(), "botfam/botfam")
	if err != nil {
		t.Fatalf("ListUnreadRepoNotifications: %v", err)
	}
	if len(ns) != 1 || ns[0].ID != 7 || ns[0].Repository.FullName != "botfam/botfam" {
		t.Errorf("unexpected notifications: %+v", ns)
	}
}

func TestGetSubject(t *testing.T) {
	c := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/botfam/botfam/issues/11" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"title":"T","body":"hello body","state":"open","html_url":"http://h/x"}`))
	})
	// Subject URL carries a DIFFERENT host (mimics ROOT_URL mismatch); GetSubject
	// must extract only the path and call the SDK using the client's base URL.
	sc, err := c.GetSubject(context.Background(), "http://wrong-host:9999/api/v1/repos/botfam/botfam/issues/11")
	if err != nil {
		t.Fatalf("GetSubject: %v", err)
	}
	if sc.Body != "hello body" || sc.State != "open" {
		t.Errorf("unexpected subject content: %+v", sc)
	}
}

func TestMarkNotificationRead(t *testing.T) {
	c := fakeForge(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/v1/notifications/threads/5" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.MarkNotificationRead(context.Background(), 5); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
}
