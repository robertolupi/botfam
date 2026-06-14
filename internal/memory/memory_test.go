package memory

import (
	"strings"
	"testing"
)

func TestRenderParseRoundTrip(t *testing.T) {
	m := Memory{
		Title:      "Discovery resolution over system mounts",
		Status:     StatusLive,
		Authors:    []string{"claude", "agy"},
		Created:    "2026-06-14",
		Updated:    "2026-06-15",
		Scope:      ScopeFam,
		Type:       TypeProject,
		Concepts:   []string{"discovery-resolution", "irc-identity"},
		Supersedes: []string{"memory-old-fact"},
		Body:       "The MCP server resolves the work dir over a tier chain.\n\n**Why:** system-wide mounts run with CWD=/.",
	}
	got, err := Parse(m.Render())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Title != m.Title {
		t.Errorf("Title = %q, want %q", got.Title, m.Title)
	}
	if got.Status != m.Status {
		t.Errorf("Status = %q, want %q", got.Status, m.Status)
	}
	if strings.Join(got.Authors, ",") != "claude,agy" {
		t.Errorf("Authors = %v", got.Authors)
	}
	if got.Created != "2026-06-14" || got.Updated != "2026-06-15" {
		t.Errorf("dates = (%q, %q)", got.Created, got.Updated)
	}
	if got.Scope != ScopeFam || got.Type != TypeProject {
		t.Errorf("scope/type = (%q, %q)", got.Scope, got.Type)
	}
	if strings.Join(got.Concepts, ",") != "discovery-resolution,irc-identity" {
		t.Errorf("Concepts = %v", got.Concepts)
	}
	if strings.Join(got.Supersedes, ",") != "memory-old-fact" {
		t.Errorf("Supersedes = %v", got.Supersedes)
	}
	if got.Body != m.Body {
		t.Errorf("Body = %q, want %q", got.Body, m.Body)
	}
}

func TestRenderMinimal(t *testing.T) {
	m := Memory{Title: "t", Body: "b"}
	out := m.Render()
	// Status defaults to Live, body is preserved.
	if !strings.Contains(out, "# t\n") {
		t.Errorf("missing title: %q", out)
	}
	if !strings.Contains(out, "Status: **Live**") {
		t.Errorf("missing default status: %q", out)
	}
	if !strings.HasSuffix(out, "b\n") {
		t.Errorf("missing body: %q", out)
	}
	// No Concepts:/Supersedes: lines when empty.
	if strings.Contains(out, "Concepts:") || strings.Contains(out, "Supersedes:") {
		t.Errorf("emitted empty optional lines: %q", out)
	}
}

func TestParseTolerant(t *testing.T) {
	// Hand-edited page: wrapped banner, no bold on Status, free field order,
	// an unknown banner key, Concepts on its own line.
	page := `# Hand edited fact

Authors: agy · Status: Historical
· Type: feedback · Created: 2026-06-10 · Origin: somewhere
Concepts: a, b,  c

The body line one.
The body line two.`
	m, err := Parse(page)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "Historical" {
		t.Errorf("Status = %q, want Historical", m.Status)
	}
	if m.Type != TypeFeedback {
		t.Errorf("Type = %q", m.Type)
	}
	if m.Created != "2026-06-10" {
		t.Errorf("Created = %q", m.Created)
	}
	if strings.Join(m.Authors, ",") != "agy" {
		t.Errorf("Authors = %v", m.Authors)
	}
	if strings.Join(m.Concepts, ",") != "a,b,c" {
		t.Errorf("Concepts = %v (whitespace must be trimmed)", m.Concepts)
	}
	if m.Body != "The body line one.\nThe body line two." {
		t.Errorf("Body = %q", m.Body)
	}
}

func TestParseRequiresTitle(t *testing.T) {
	if _, err := Parse("no title here\n\nbody"); err == nil {
		t.Error("expected error for missing H1 title")
	}
}

func TestValidate(t *testing.T) {
	base := Memory{Title: "t", Body: "b", Authors: []string{"claude"}, Created: "2026-06-14"}
	if err := base.Validate(); err != nil {
		t.Errorf("valid memory rejected: %v", err)
	}
	cases := map[string]Memory{
		"no title":   {Body: "b", Authors: []string{"claude"}, Created: "2026-06-14"},
		"no body":    {Title: "t", Authors: []string{"claude"}, Created: "2026-06-14"},
		"no authors": {Title: "t", Body: "b", Created: "2026-06-14"},
		"no created": {Title: "t", Body: "b", Authors: []string{"claude"}},
		"bad scope":  {Title: "t", Body: "b", Authors: []string{"claude"}, Created: "2026-06-14", Scope: "world"},
		"bad type":   {Title: "t", Body: "b", Authors: []string{"claude"}, Created: "2026-06-14", Type: "rumor"},
	}
	for name, m := range cases {
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Discovery resolution over mounts": "memory-discovery-resolution-over-mounts",
		"IRC identity / nick scoping":      "memory-irc-identity-nick-scoping",
		"  Trailing!!  ":                   "memory-trailing",
		"memory-already-prefixed":          "memory-already-prefixed",
		"":                                 "",
		"!!!":                              "",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortAuthors(t *testing.T) {
	got := SortAuthors([]string{"claude", "agy", "claude", " ", "new-claude"})
	if strings.Join(got, ",") != "agy,claude,new-claude" {
		t.Errorf("SortAuthors = %v", got)
	}
}
