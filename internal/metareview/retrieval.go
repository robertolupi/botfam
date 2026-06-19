package metareview

import (
	"regexp"
	"sort"
	"strings"
)

// Artifact is the forge item under review (an issue or a PR).
type Artifact struct {
	Number int
	Kind   string // "issue" | "pr"
	Title  string
	Body   string
	Diff   string // unified diff; PRs only, empty for issues
}

// Corpus is the repo state the deterministic detection checks against. It is an
// interface so detection is pure and unit-testable without a checkout or forge.
type Corpus interface {
	// FileExists reports whether path (repo-relative file or dir) exists in the
	// tree at the artifact's tip.
	FileExists(path string) bool
	// Lineage returns the wiki Lineage page content (for the superseded check),
	// or "" if it could not be fetched.
	Lineage() string
}

// Candidate is a deterministically-detected risk signal carrying the raw
// evidence the driver gathered. The model later confirms (keep/drop) and
// rephrases it; the driver never posts a candidate the model did not confirm.
type Candidate struct {
	Label    string // one of the per-artifact labels
	Evidence string // cited evidence the driver found
	Triage   string // suggested triage/* (may be empty)
	Escalate bool   // judgment-heavy: prefer the stronger --escalate model
	// EscalateOnly candidates are dropped entirely when no --escalate model is
	// configured (not surfaced at low confidence). hollow-validation is the case:
	// the deterministic pass cannot distinguish it from an ordinary literal
	// assertion, so without a strong model to do the real reasoning it is pure
	// noise (PR botfam#328 dogfood). speculative, which fires on specific cues,
	// is not EscalateOnly — it still runs locally at low confidence.
	EscalateOnly bool
}

// pathRefRe matches a backtick- or paren-delimited repo path: a token with at
// least one slash and a file extension, e.g. `internal/trace/schema.go` or
// (doc/foo.md). Anchored on the delimiter to avoid grabbing prose.
var pathRefRe = regexp.MustCompile("[`(]([A-Za-z0-9_][A-Za-z0-9_./-]*\\.[A-Za-z0-9]+)[`)]")

// knownDirRe matches a backticked directory reference under a known top-level
// repo dir (e.g. `internal/trace/`), which has no extension to match on.
var knownDirRe = regexp.MustCompile("`((?:internal|cmd|doc|skills|tools|wiki|docker|third_party)/[A-Za-z0-9_./-]+)`")

// diffAddedPathRe matches the new-file path on a unified-diff "+++ b/..." line.
var diffAddedPathRe = regexp.MustCompile(`(?m)^\+\+\+ b/(.+)$`)

// codeExts marks tokens that are repo artifacts even without a path separator
// (e.g. `main.go`). A bare single-segment name with another extension — most
// importantly `Foo.md` — is treated as a wiki page, not a repo file, so a
// superseded-decision link does not masquerade as a missing file.
var codeExts = map[string]bool{
	".go": true, ".sh": true, ".py": true, ".ts": true, ".js": true,
	".rs": true, ".toml": true, ".yaml": true, ".yml": true, ".mod": true, ".sum": true,
}

// referencedPaths extracts repo-path-like tokens from text, de-duplicated and
// sorted for determinism. URLs are excluded by the delimiter anchoring; a token
// is kept only if it carries a path separator or a known code extension (so
// wiki-page links are not mistaken for repo files).
func referencedPaths(text string) []string {
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSuffix(strings.TrimSpace(p), "/")
		if p == "" || strings.Contains(p, "://") {
			return
		}
		if !strings.Contains(p, "/") {
			if dot := strings.LastIndexByte(p, '.'); dot < 0 || !codeExts[strings.ToLower(p[dot:])] {
				return
			}
		}
		seen[p] = true
	}
	for _, m := range pathRefRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, m := range knownDirRe.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// diffAddedPaths returns the set of files a diff adds (so a PR is not flagged
// for "referencing" a file it itself creates).
func diffAddedPaths(diff string) map[string]bool {
	added := map[string]bool{}
	for _, m := range diffAddedPathRe.FindAllStringSubmatch(diff, -1) {
		added[strings.TrimSpace(m[1])] = true
	}
	return added
}

// detectPhaseInversion flags referenced repo paths absent from the tree (and,
// for a PR, not created by its own diff). Mechanical and high-confidence.
func detectPhaseInversion(art Artifact, corpus Corpus) []Candidate {
	added := diffAddedPaths(art.Diff)
	var cands []Candidate
	for _, p := range referencedPaths(art.Title + "\n" + art.Body) {
		if added[p] || corpus.FileExists(p) {
			continue
		}
		cands = append(cands, Candidate{
			Label:    LabelPhaseInversion,
			Evidence: "references `" + p + "`, absent from the tree and merged PRs",
			Triage:   suggestedTriage[LabelPhaseInversion],
		})
	}
	return cands
}

// supersedeWordRe matches the words Lineage uses to mark a decision retired.
var supersedeWordRe = regexp.MustCompile(`(?i)supersed|retired|reversed|replaced|tabled|pivoted`)

// decisionRefRe matches a referenced design-decision page slug: a markdown link
// target ending in .md, or a backticked lowercase hyphenated slug (the shape of
// proposal-*/lineage-* wiki pages).
var (
	mdLinkRe   = regexp.MustCompile(`\(([a-z0-9][a-z0-9-]*?)\.md\)`)
	slugRefRe  = regexp.MustCompile("`([a-z0-9]+(?:-[a-z0-9]+){2,})`")
	wikiLinkRe = regexp.MustCompile(`\[\[([a-z0-9][a-z0-9-]*)\]\]`)
)

// referencedDecisions extracts candidate decision/page slugs from text.
func referencedDecisions(text string) []string {
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{mdLinkRe, slugRefRe, wikiLinkRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			seen[strings.TrimSuffix(m[1], ".md")] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// detectSuperseded flags a referenced decision whose Lineage line marks it
// superseded. Mechanical: the evidence is the Lineage line itself.
func detectSuperseded(art Artifact, corpus Corpus) []Candidate {
	lineage := corpus.Lineage()
	if strings.TrimSpace(lineage) == "" {
		return nil
	}
	lines := strings.Split(lineage, "\n")
	var cands []Candidate
	for _, slug := range referencedDecisions(art.Title + "\n" + art.Body) {
		for _, ln := range lines {
			if strings.Contains(ln, slug) && supersedeWordRe.MatchString(ln) {
				cands = append(cands, Candidate{
					Label:    LabelSuperseded,
					Evidence: "builds on `" + slug + "`, marked superseded in Lineage: " + condense(ln),
					Triage:   suggestedTriage[LabelSuperseded],
				})
				break
			}
		}
	}
	return cands
}

// hollowAssertRe matches an added test line that *asserts a literal* — the
// shape most prone to re-asserting what the impl emits (skill #295 scar). It
// targets concrete assertion shapes, not the bare words: a testify
// require/assert call, an `if got != <literal>` guard, or a table-test
// `want := <literal>` binding. This deliberately excludes diagnostic strings
// like t.Errorf("...want %d...") where the word only appears inside a message
// (the dogfood false positive on PR botfam#328).
var hollowAssertRe = regexp.MustCompile(`(?:require|assert)\.\w+\(|\bif\s+\w[\w.]*\s*!=\s*["0-9]|\b(?:want|expected)\w*\s*[:=]+\s*["0-9]`)

// detectHollowValidation extracts added test assertions as LOW-confidence,
// escalate-only candidates. The driver never trusts these silently: the model
// (escalated to a stronger one) must do the real reasoning, or they are dropped
// (skill: "either escalated or explicitly low-confidence, not silently trusted").
func detectHollowValidation(art Artifact) []Candidate {
	if art.Diff == "" {
		return nil
	}
	var cands []Candidate
	var file string
	for _, ln := range strings.Split(art.Diff, "\n") {
		if m := diffAddedPathRe.FindStringSubmatch(ln); m != nil {
			file = strings.TrimSpace(m[1])
			continue
		}
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		if !strings.HasPrefix(ln, "+") { // only added lines
			continue
		}
		if hollowAssertRe.MatchString(ln) {
			cands = append(cands, Candidate{
				Label:        LabelHollowValidation,
				Evidence:     "in `" + file + "`, assertion may re-assert what the impl emits: " + condense(strings.TrimPrefix(ln, "+")),
				Escalate:     true,
				EscalateOnly: true,
			})
			if len(cands) >= 3 { // cap noise — a few examples are enough for the model
				break
			}
		}
	}
	return cands
}

// speculativeCueRe matches phrasing that hints the least-grounded piece is being
// built ahead of its justification.
var speculativeCueRe = regexp.MustCompile(`(?i)\b(?:speculative|no data yet|before we have|assum\w+ that|once .* exists|not yet proven|unvalidated assumption)\b`)

// detectSpeculative is a light heuristic; it always escalates (judgment), and
// only fires when a cue is plainly present in the artifact.
func detectSpeculative(art Artifact) []Candidate {
	body := art.Title + "\n" + art.Body
	if m := speculativeCueRe.FindString(body); m != "" {
		return []Candidate{{
			Label:    LabelSpeculative,
			Evidence: "artifact signals an unvalidated foundation (cue: \"" + strings.TrimSpace(m) + "\")",
			Escalate: true,
		}}
	}
	return nil
}

// Detect runs every deterministic detector and returns the candidate signals in
// a stable order. An empty result means a clean artifact (nothing to confirm).
func Detect(art Artifact, corpus Corpus) []Candidate {
	var cands []Candidate
	cands = append(cands, detectPhaseInversion(art, corpus)...)
	cands = append(cands, detectSuperseded(art, corpus)...)
	cands = append(cands, detectHollowValidation(art)...)
	cands = append(cands, detectSpeculative(art)...)
	return cands
}

// condense collapses whitespace and truncates a quoted evidence fragment so it
// stays a one-liner in the comment table.
func condense(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 120
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
