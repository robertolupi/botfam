// Package wiki serves the live forge wiki (proposals, reviews, sessions,
// lineage) as a self-contained provider over the forge API, with a flagged
// local-cache fallback. The forge wiki is the source of truth; a stale local
// clone is never authoritative (#119).
package wiki

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/robertolupi/botfam/internal/forge"
)

// pageNamePattern constrains wiki page names to the forge wiki namespace: no
// slashes, no traversal, no arbitrary filesystem reach.
var pageNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidPageName reports whether name is a safe forge wiki page name.
func ValidPageName(name string) bool { return pageNamePattern.MatchString(name) }

// Page is a single wiki page plus provenance metadata.
type Page struct {
	Name    string `json:"page"`
	Content string `json:"content"`
	SHA     string `json:"sha,omitempty"`
	Updated string `json:"updated,omitempty"`
	Source  string `json:"source"`          // "gitea" | "local-cache"
	Stale   bool   `json:"stale,omitempty"` // true for the cache tier
}

// PageMeta is an index entry (no content).
type PageMeta struct {
	Name    string `json:"page"`
	URI     string `json:"uri"`
	SHA     string `json:"sha,omitempty"`
	Updated string `json:"updated,omitempty"`
	Source  string `json:"source"`
}

// Provider reads wiki pages from one backing store.
type Provider interface {
	Page(name string) (Page, error)
	Index() ([]PageMeta, error)
	Source() string
}

// ForgeProvider reads the live wiki via the forge API using the shared
// forge.Client (token, timeout, injectable transport).
type ForgeProvider struct{ C *forge.Client }

func (p ForgeProvider) Source() string { return "gitea" }

type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
}

type apiPage struct {
	Title         string    `json:"title"`
	ContentBase64 string    `json:"content_base64"`
	SubURL        string    `json:"sub_url"`
	LastCommit    apiCommit `json:"last_commit"`
}

func (p ForgeProvider) Page(name string) (Page, error) {
	if !ValidPageName(name) {
		return Page{}, fmt.Errorf("invalid wiki page name %q", name)
	}
	path := fmt.Sprintf("repos/%s/%s/wiki/page/%s", p.C.Owner, p.C.Repo, url.PathEscape(name))
	body, err := p.C.Request("GET", path, nil)
	if err != nil {
		return Page{}, err
	}
	var ap apiPage
	if err := json.Unmarshal(body, &ap); err != nil {
		return Page{}, fmt.Errorf("decode wiki page %q: %w", name, err)
	}
	content, err := base64.StdEncoding.DecodeString(ap.ContentBase64)
	if err != nil {
		return Page{}, fmt.Errorf("decode wiki content %q: %w", name, err)
	}
	return Page{
		Name:    name,
		Content: string(content),
		SHA:     ap.LastCommit.SHA,
		Updated: ap.LastCommit.Commit.Author.Date,
		Source:  "gitea",
	}, nil
}

func (p ForgeProvider) Index() ([]PageMeta, error) {
	path := fmt.Sprintf("repos/%s/%s/wiki/pages", p.C.Owner, p.C.Repo)
	body, err := p.C.Request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var pages []apiPage
	if err := json.Unmarshal(body, &pages); err != nil {
		return nil, fmt.Errorf("decode wiki index: %w", err)
	}
	metas := make([]PageMeta, 0, len(pages))
	for _, ap := range pages {
		metas = append(metas, PageMeta{
			Name:    ap.Title,
			URI:     "botfam:///wiki/" + ap.Title,
			SHA:     ap.LastCommit.SHA,
			Updated: ap.LastCommit.Commit.Author.Date,
			Source:  "gitea",
		})
	}
	return metas, nil
}

// CacheProvider reads an already-cloned local wiki/ directory. It always marks
// results stale: the operator must have synced it, and it may be behind the
// forge.
type CacheProvider struct{ Dir string }

func (p CacheProvider) Source() string { return "local-cache" }

func (p CacheProvider) Page(name string) (Page, error) {
	if !ValidPageName(name) {
		return Page{}, fmt.Errorf("invalid wiki page name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(p.Dir, name+".md"))
	if err != nil {
		return Page{}, err
	}
	return Page{Name: name, Content: string(b), Source: "local-cache", Stale: true}, nil
}

func (p CacheProvider) Index() ([]PageMeta, error) {
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, err
	}
	var metas []PageMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		metas = append(metas, PageMeta{
			Name:   name,
			URI:    "botfam:///wiki/" + name,
			Source: "local-cache",
		})
	}
	return metas, nil
}

// Resolve picks the live provider, falling back to a flagged-stale local cache.
// A nil client (no forge URL/token) skips the live tier. When nothing is
// resolvable it returns a clear diagnostic so the caller can surface it.
//
// The git-clone tier (pull the .wiki.git remote) is a future extension point
// between forge and cache; see OQ-W3 on the wiki-provider design page.
func Resolve(client *forge.Client, cacheDir string) (Provider, error) {
	if client != nil {
		return ForgeProvider{C: client}, nil
	}
	if cacheDir != "" {
		if fi, err := os.Stat(cacheDir); err == nil && fi.IsDir() {
			return CacheProvider{Dir: cacheDir}, nil
		}
	}
	return nil, fmt.Errorf("no wiki source: forge token/URL unresolved and no local wiki cache at %q", cacheDir)
}
