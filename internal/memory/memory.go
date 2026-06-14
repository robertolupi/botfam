// Package memory implements the shared-memory fact schema for a multi-harness
// fam (proposal-shared-memory-multi-harness, P1). One fact = one wiki page so
// disjoint facts touch disjoint pages and merge without conflict; this package
// owns only the page format (render + parse), not storage.
package memory

import (
	"fmt"
	"sort"
	"strings"
)

// Scope says who a fact belongs to.
const (
	ScopePersonal = "personal"
	ScopeFam      = "fam"
	ScopeCrossFam = "cross-fam"
)

// Type keeps Claude's four memory types.
const (
	TypeUser      = "user"
	TypeFeedback  = "feedback"
	TypeProject   = "project"
	TypeReference = "reference"
)

// Status freshness.
const (
	StatusLive       = "Live"
	StatusHistorical = "Historical"
)

// Memory is one fact. It renders to / parses from a wiki page that uses the
// `Status:`/`Authors:` banner convention (not YAML) shared by the fam wiki.
type Memory struct {
	Title      string
	Status     string   // Live | Historical
	Authors    []string // attribution; git blame is the audit trail
	Created    string   // YYYY-MM-DD
	Updated    string   // YYYY-MM-DD (optional)
	Scope      string   // personal | fam | cross-fam
	Type       string   // user | feedback | project | reference
	Concepts   []string // seeds the @concept retrieval graph (optional)
	Supersedes []string // slugs of facts this replaces (optional)
	Body       string   // the fact itself
}

// Render serializes m to its canonical wiki-page markdown. The banner mirrors
// the existing fam-wiki convention: an H1 title, a dot-separated metadata line,
// optional Concepts:/Supersedes: lines, a blank line, then the body.
func (m Memory) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSpace(m.Title))

	fields := []string{fmt.Sprintf("Status: **%s**", orDefault(m.Status, StatusLive))}
	if len(m.Authors) > 0 {
		fields = append(fields, "Authors: "+strings.Join(m.Authors, ", "))
	}
	if m.Created != "" {
		fields = append(fields, "Created: "+m.Created)
	}
	if m.Updated != "" {
		fields = append(fields, "Updated: "+m.Updated)
	}
	if m.Scope != "" {
		fields = append(fields, "Scope: "+m.Scope)
	}
	if m.Type != "" {
		fields = append(fields, "Type: "+m.Type)
	}
	b.WriteString(strings.Join(fields, " · "))
	b.WriteString("\n")

	if len(m.Concepts) > 0 {
		fmt.Fprintf(&b, "Concepts: %s\n", strings.Join(m.Concepts, ", "))
	}
	if len(m.Supersedes) > 0 {
		fmt.Fprintf(&b, "Supersedes: %s\n", strings.Join(m.Supersedes, ", "))
	}

	body := strings.TrimRight(m.Body, "\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// Parse reads a memory page. It is tolerant: the metadata line may wrap across
// physical lines (the renderer keeps it on one, but hand-edited pages may not),
// field order is free, **bold** around the Status value is optional, and
// unknown banner keys are ignored. The H1 title and a non-empty body are the
// only hard requirements.
func Parse(data string) (Memory, error) {
	lines := strings.Split(data, "\n")

	// Title: the first H1.
	var m Memory
	i := 0
	for ; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "# ") {
			m.Title = strings.TrimSpace(strings.TrimPrefix(lines[i], "# "))
			i++
			break
		}
	}
	if m.Title == "" {
		return Memory{}, fmt.Errorf("memory page has no H1 title")
	}

	// Skip blank lines between the title and the banner.
	for ; i < len(lines) && strings.TrimSpace(lines[i]) == ""; i++ {
	}

	// Header block: consecutive non-empty lines form the banner. Concepts: and
	// Supersedes: own their lines; everything else is dot-separated key:value.
	var bannerParts []string
	for ; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
		line := strings.TrimSpace(lines[i])
		switch {
		case strings.HasPrefix(line, "Concepts:"):
			m.Concepts = splitList(strings.TrimPrefix(line, "Concepts:"))
		case strings.HasPrefix(line, "Supersedes:"):
			m.Supersedes = splitList(strings.TrimPrefix(line, "Supersedes:"))
		default:
			bannerParts = append(bannerParts, line)
		}
	}
	applyBanner(&m, strings.Join(bannerParts, " · "))

	// Body: the remainder after the blank line that closes the header.
	for ; i < len(lines) && strings.TrimSpace(lines[i]) == ""; i++ {
	}
	m.Body = strings.TrimRight(strings.Join(lines[i:], "\n"), "\n")
	return m, nil
}

// applyBanner parses the dot-separated "Key: value" metadata fields.
func applyBanner(m *Memory, banner string) {
	for _, field := range strings.Split(banner, "·") {
		field = strings.TrimSpace(field)
		key, val, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "Status":
			m.Status = strings.Trim(val, "* ")
		case "Authors":
			m.Authors = splitList(val)
		case "Created":
			m.Created = val
		case "Updated":
			m.Updated = val
		case "Scope":
			m.Scope = val
		case "Type":
			m.Type = val
		}
	}
}

// splitList parses a comma-separated banner value into trimmed, non-empty items.
func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// ValidScope / ValidType report whether a value is a known enum member.
func ValidScope(s string) bool {
	switch s {
	case ScopePersonal, ScopeFam, ScopeCrossFam:
		return true
	}
	return false
}

func ValidType(t string) bool {
	switch t {
	case TypeUser, TypeFeedback, TypeProject, TypeReference:
		return true
	}
	return false
}

// Validate checks the required fields and enum values for a fact about to be
// written. Authors/Created are required for attribution + freshness.
func (m Memory) Validate() error {
	if strings.TrimSpace(m.Title) == "" {
		return fmt.Errorf("memory requires a title")
	}
	if strings.TrimSpace(m.Body) == "" {
		return fmt.Errorf("memory %q requires a body", m.Title)
	}
	if len(m.Authors) == 0 {
		return fmt.Errorf("memory %q requires at least one author", m.Title)
	}
	if m.Created == "" {
		return fmt.Errorf("memory %q requires a Created date", m.Title)
	}
	if m.Scope != "" && !ValidScope(m.Scope) {
		return fmt.Errorf("memory %q has invalid scope %q", m.Title, m.Scope)
	}
	if m.Type != "" && !ValidType(m.Type) {
		return fmt.Errorf("memory %q has invalid type %q", m.Title, m.Type)
	}
	return nil
}

// Slug derives the addressable wiki page slug from a title. Hyphenated, not
// space-separated, to stay reachable until the wiki Index() multi-word
// addressing bug (#131) is fixed. The "memory-" prefix namespaces facts so the
// `memory-*` projection can enumerate them.
func Slug(title string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return ""
	}
	if strings.HasPrefix(slug, "memory-") {
		return slug
	}
	return "memory-" + slug
}

// SortAuthors returns a deduplicated, sorted author list (stable rendering when
// multiple agents co-author a fact).
func SortAuthors(authors []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range authors {
		a = strings.TrimSpace(a)
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}
