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

func TestListAllUnreadNotificationsPaginates(t *testing.T) {
	var pages []int
	c := &Client{
		BaseURL: "http://forge.test", Token: "t",
		HTTPClient: fakeClient(func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
			pages = append(pages, len(pages)+1)
			w.WriteHeader(http.StatusOK)
			switch page {
			case "1":
				// A full page (limit=50) must trigger a follow-up fetch.
				var b []byte
				b = append(b, '[')
				for i := 1; i <= 50; i++ {
					if i > 1 {
						b = append(b, ',')
					}
					b = append(b, []byte(`{"id":`+itoa(i)+`,"unread":true,"subject":{"type":"Issue"},"repository":{"full_name":"botfam/botfam"}}`)...)
				}
				b = append(b, ']')
				_, _ = w.Write(b)
			case "2":
				// A short page ends pagination.
				_, _ = w.Write([]byte(`[{"id":51,"unread":true,"subject":{"type":"Issue"},"repository":{"full_name":"botfam/botfam"}}]`))
			default:
				t.Errorf("unexpected extra page request: %s", page)
				_, _ = w.Write([]byte(`[]`))
			}
		}),
	}
	ns, err := c.ListAllUnreadNotifications()
	if err != nil {
		t.Fatalf("ListAllUnreadNotifications: %v", err)
	}
	if len(ns) != 51 {
		t.Fatalf("got %d notifications across pages, want 51", len(ns))
	}
	if len(pages) != 2 {
		t.Errorf("made %d page requests, want exactly 2 (stop on short page)", len(pages))
	}
	if ns[50].ID != 51 {
		t.Errorf("last notification id = %d, want 51 (from page 2)", ns[50].ID)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
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
