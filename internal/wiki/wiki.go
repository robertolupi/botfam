// Package wiki serves the live forge wiki (proposals, reviews, sessions,
// lineage) as a self-contained provider over the forge API, with a flagged
// local-cache fallback. The forge wiki is the source of truth; a stale local
// clone is never authoritative (#119).
package wiki

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path"
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

func (p ForgeProvider) Page(name string) (Page, error) {
	if !ValidPageName(name) {
		return Page{}, fmt.Errorf("invalid wiki page name %q", name)
	}
	ctx := context.Background()
	wp, err := p.C.GetWikiPage(ctx, name)
	if err != nil && !strings.HasSuffix(name, ".-") {
		if fb, fbErr := p.C.GetWikiPage(ctx, name+".-"); fbErr == nil {
			wp, err = fb, nil
		}
	}
	if err != nil {
		return Page{}, err
	}
	content, err := base64.StdEncoding.DecodeString(wp.ContentBase64)
	if err != nil {
		return Page{}, fmt.Errorf("decode wiki content %q: %w", name, err)
	}
	return Page{
		Name:    name,
		Content: string(content),
		SHA:     wp.CommitSHA,
		Updated: wp.CommitDate,
		Source:  "gitea",
	}, nil
}

func (p ForgeProvider) Index() ([]PageMeta, error) {
	pages, err := p.C.ListWikiPages(context.Background())
	if err != nil {
		return nil, err
	}
	metas := make([]PageMeta, 0, len(pages))
	for _, mp := range pages {
		name := mp.SubURL
		if name == "" {
			name = mp.Title
		}
		name = strings.TrimSuffix(name, ".-")
		metas = append(metas, PageMeta{
			Name:    name,
			URI:     "botfam:///wiki/" + name,
			SHA:     mp.CommitSHA,
			Updated: mp.CommitDate,
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
	filePath := filepath.Join(p.Dir, name+".md")
	b, err := os.ReadFile(filePath)
	if err != nil && os.IsNotExist(err) && !strings.HasSuffix(name, ".-") {
		fallbackPath := filepath.Join(p.Dir, name+".-.md")
		if fallbackBytes, fallbackErr := os.ReadFile(fallbackPath); fallbackErr == nil {
			b = fallbackBytes
			err = nil
		}
	}
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
		name = strings.TrimSuffix(name, ".-")
		metas = append(metas, PageMeta{
			Name:   name,
			URI:    "botfam:///wiki/" + name,
			Source: "local-cache",
		})
	}
	return metas, nil
}

// Projection is a fam-declared curated index: a name plus a glob matched
// against flat wiki page names (e.g. {Name:"reviews", Match:"review-*"}).
type Projection struct {
	Name  string
	Match string
}

// ParseProjections parses fam.toml "name:glob" entries into Projections,
// skipping malformed ones. Names are constrained like page names so a
// projection can be safely addressed as botfam:///<name>.
func ParseProjections(entries []string) []Projection {
	var ps []Projection
	for _, e := range entries {
		name, glob, ok := strings.Cut(e, ":")
		name, glob = strings.TrimSpace(name), strings.TrimSpace(glob)
		if !ok || name == "" || glob == "" || !ValidPageName(name) {
			continue
		}
		ps = append(ps, Projection{Name: name, Match: glob})
	}
	return ps
}

// Filter returns the index entries whose page name matches glob (path.Match
// semantics), preserving order.
func Filter(metas []PageMeta, glob string) []PageMeta {
	var out []PageMeta
	for _, m := range metas {
		if ok, err := path.Match(glob, m.Name); err == nil && ok {
			out = append(out, m)
		}
	}
	return out
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
