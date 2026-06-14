package wiki

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertolupi/botfam/internal/forge"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeForgeClient builds a forge.Client whose transport serves handler in
// memory (no TCP listener).
func fakeForgeClient(handler http.HandlerFunc) *forge.Client {
	return &forge.Client{
		BaseURL: "http://forge.test",
		Owner:   "o",
		Repo:    "r",
		Token:   "t",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			handler(rec, req)
			return rec.Result(), nil
		})},
	}
}

func TestValidPageName(t *testing.T) {
	for _, ok := range []string{"Home", "review-2026-06-14-agy", "proposal-mcp-x", "a.b_c"} {
		if !ValidPageName(ok) {
			t.Errorf("expected %q valid", ok)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", "/etc/passwd", ".hidden", "a b"} {
		if ValidPageName(bad) {
			t.Errorf("expected %q invalid", bad)
		}
	}
}

func TestForgeProviderPage(t *testing.T) {
	content := "# Home\nhello wiki\n"
	p := ForgeProvider{C: fakeForgeClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/o/r/wiki/page/Home" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"title":"Home","content_base64":"` +
			base64.StdEncoding.EncodeToString([]byte(content)) +
			`","last_commit":{"sha":"abc123","commit":{"author":{"date":"2026-06-14T00:00:00Z"}}}}`))
	})}

	page, err := p.Page("Home")
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if page.Content != content {
		t.Errorf("content = %q, want %q", page.Content, content)
	}
	if page.SHA != "abc123" || page.Source != "gitea" || page.Stale {
		t.Errorf("unexpected metadata: %+v", page)
	}
}

func TestForgeProviderRejectsBadName(t *testing.T) {
	p := ForgeProvider{C: fakeForgeClient(func(w http.ResponseWriter, r *http.Request) {
		t.Error("transport must not be hit for an invalid name")
	})}
	if _, err := p.Page("../../etc/passwd"); err == nil {
		t.Fatal("expected error for traversal name")
	}
}

func TestForgeProviderIndex(t *testing.T) {
	p := ForgeProvider{C: fakeForgeClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/o/r/wiki/pages" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte(`[{"title":"Home","last_commit":{"sha":"s1"}},{"title":"Proposals","last_commit":{"sha":"s2"}}]`))
	})}
	metas, err := p.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 2 || metas[0].Name != "Home" || metas[0].URI != "botfam:///wiki/Home" || metas[1].SHA != "s2" {
		t.Errorf("unexpected index: %+v", metas)
	}
}

func TestCacheProviderIsStale(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Home.md"), []byte("cached home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := CacheProvider{Dir: dir}
	page, err := p.Page("Home")
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if !page.Stale || page.Source != "local-cache" || !strings.Contains(page.Content, "cached home") {
		t.Errorf("expected stale local-cache page, got %+v", page)
	}
	metas, err := p.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(metas) != 1 || metas[0].Name != "Home" || metas[0].Source != "local-cache" {
		t.Errorf("unexpected cache index: %+v", metas)
	}
}

func TestResolve(t *testing.T) {
	// nil client + existing cache dir -> cache provider.
	dir := t.TempDir()
	prov, err := Resolve(nil, dir)
	if err != nil {
		t.Fatalf("Resolve with cache: %v", err)
	}
	if prov.Source() != "local-cache" {
		t.Errorf("expected local-cache, got %s", prov.Source())
	}
	// nil client + no cache -> clear error.
	if _, err := Resolve(nil, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error when no source is resolvable")
	}
	// client present -> forge provider.
	prov, err = Resolve(fakeForgeClient(func(http.ResponseWriter, *http.Request) {}), "")
	if err != nil {
		t.Fatalf("Resolve with client: %v", err)
	}
	if prov.Source() != "gitea" {
		t.Errorf("expected gitea, got %s", prov.Source())
	}
}
