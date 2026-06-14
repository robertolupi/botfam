package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/robertolupi/botfam/internal/docs"
	"github.com/robertolupi/botfam/internal/fam"
	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/wiki"
)

// wikiProvider resolves the live wiki provider for workDir: the forge API when
// a client (forge URL + token) is resolvable, else a flagged-stale local
// wiki/ cache, else a clear diagnostic.
func wikiProvider(workDir, actor string) (wiki.Provider, error) {
	var client *forge.Client
	if c, err := forge.NewClient(workDir, actor); err == nil {
		client = c
	}
	return wiki.Resolve(client, filepath.Join(workDir, "wiki"))
}

// renderWikiIndexMarkdown lists wiki pages as a compact markdown index.
func renderWikiIndexMarkdown(metas []wiki.PageMeta, source string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Wiki index (source: %s)\n\n", source)
	if len(metas) == 0 {
		b.WriteString("_No pages._\n")
	}
	for _, m := range metas {
		fmt.Fprintf(&b, "- `%s` → `%s`\n", m.Name, m.URI)
	}
	return []byte(b.String())
}

type wikiIndexJSON struct {
	Schema string          `json:"schema"`
	Source string          `json:"source"`
	Pages  []wiki.PageMeta `json:"pages"`
}

func renderWikiIndexJSON(metas []wiki.PageMeta, source string) ([]byte, error) {
	return json.MarshalIndent(wikiIndexJSON{
		Schema: "botfam.wiki.index.v1",
		Source: source,
		Pages:  metas,
	}, "", "  ")
}

// renderWikiPage renders a fetched page as markdown with a provenance footer.
func renderWikiPage(p wiki.Page) []byte {
	var b strings.Builder
	b.Write([]byte(p.Content))
	if !strings.HasSuffix(p.Content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "_source: %s", p.Source)
	if p.SHA != "" {
		fmt.Fprintf(&b, " · sha: %s", p.SHA)
	}
	if p.Updated != "" {
		fmt.Fprintf(&b, " · updated: %s", p.Updated)
	}
	if p.Stale {
		b.WriteString(" · ⚠️ STALE (local cache; the forge may be ahead)")
	}
	b.WriteString("_\n")
	return []byte(b.String())
}

// renderProjectionMarkdown lists the wiki pages matching a fam-declared
// projection.
func renderProjectionMarkdown(name, match, source string, metas []wiki.PageMeta) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (source: %s, match: `%s`)\n\n", name, source, match)
	if len(metas) == 0 {
		b.WriteString("_No matching pages._\n")
	}
	for _, m := range metas {
		fmt.Fprintf(&b, "- `%s` → `%s`\n", m.Name, m.URI)
	}
	return []byte(b.String())
}

type projectionJSON struct {
	Schema     string          `json:"schema"`
	Projection string          `json:"projection"`
	Match      string          `json:"match"`
	Source     string          `json:"source"`
	Pages      []wiki.PageMeta `json:"pages"`
}

func renderProjectionJSON(name, match, source string, metas []wiki.PageMeta) ([]byte, error) {
	return json.MarshalIndent(projectionJSON{
		Schema:     "botfam.projection.v1",
		Projection: name,
		Match:      match,
		Source:     source,
		Pages:      metas,
	}, "", "  ")
}

// markdownResource wraps embedded/rendered markdown as MCP resource contents.
func markdownResource(uri string, content []byte) []mcplib.ResourceContents {
	return []mcplib.ResourceContents{mcplib.TextResourceContents{
		URI:      uri,
		MIMEType: "text/markdown",
		Text:     string(content),
	}}
}

// discoverySlugs is the ordered set of embedded generic docs served under
// botfam:///docs/<slug>. It mirrors the corpus in internal/docs (#117).
var discoverySlugs = []string{
	"start", "protocol", "ops", "operator", "review", "worktrees", "markdown",
}

// healthCheck is one entry in the discovery root's health report. A non-"ok"
// status carries a fix telling a fresh agent exactly what to create/run.
type healthCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"` // "ok" | "warn"
	Fix    string `json:"fix,omitempty"`
}

// discoveryData bundles the runtime, fam-specific context spliced over the
// generic embedded skeleton to build the root resource. The doc template data
// is generic-by-default (placeholders) and only filled where resolvable.
type discoveryData struct {
	tmpl        docs.TemplateData
	health      []healthCheck
	projections []wiki.Projection
}

// buildDiscoveryData resolves the fam-specific runtime config for workDir. It
// never fails: unresolved fields stay empty (rendered as <placeholders>) and
// are surfaced as health warnings.
func buildDiscoveryData(workDir string) discoveryData {
	var d discoveryData

	if info, err := (fam.Resolver{WorkDir: workDir}).Resolve(); err == nil {
		d.tmpl.Actor = info.Actor
		d.tmpl.Fam = info.Name
	}
	reg := fam.LoadFamRegistry(workDir)
	if d.tmpl.Fam == "" {
		d.tmpl.Fam = fam.FamSlug(reg)
	}
	d.tmpl.MainChannel, d.tmpl.CcrepChannel = fam.FamChannels(reg)
	d.tmpl.IntegrationBranch = fam.FamBranch(reg)
	d.tmpl.ForgeURL = reg.Origin
	d.projections = wiki.ParseProjections(reg.WikiProjections)

	d.health = discoveryHealth(workDir, d.tmpl)
	return d
}

func discoveryHealth(workDir string, t docs.TemplateData) []healthCheck {
	var checks []healthCheck

	if t.Actor == "" {
		checks = append(checks, healthCheck{"actor", "warn",
			"could not resolve an actor: run from a named worktree (wt-<actor>) or set COLLAB_ACTOR"})
	} else {
		checks = append(checks, healthCheck{"actor", "ok", ""})
	}

	// Forge token: token-<fam>-<actor> or the legacy token-botfam-<actor>.
	if t.Actor != "" {
		if home, err := os.UserHomeDir(); err == nil {
			famName := t.Fam
			if famName == "" {
				famName = "botfam"
			}
			primary := filepath.Join(home, ".botfam", fmt.Sprintf("token-%s-%s", famName, t.Actor))
			legacy := filepath.Join(home, ".botfam", fmt.Sprintf("token-botfam-%s", t.Actor))
			if fileExists(primary) || fileExists(legacy) {
				checks = append(checks, healthCheck{"forge_token", "ok", ""})
			} else {
				checks = append(checks, healthCheck{"forge_token", "warn",
					fmt.Sprintf("no forge token: write your Gitea token to %s", primary)})
			}
		}
	}

	// IRC client: the live-input FIFO the client creates under scratch/irc/<actor>.
	if t.Actor != "" {
		fifo := filepath.Join(workDir, "scratch", "irc", t.Actor, "in")
		if fileExists(fifo) {
			checks = append(checks, healthCheck{"irc_client", "ok", ""})
		} else {
			checks = append(checks, healthCheck{"irc_client", "warn",
				fmt.Sprintf("IRC client not running: start `botfam irc-client %s` (see botfam:///docs/ops)", t.Actor)})
		}
	}

	return checks
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// renderRoot builds the human-readable botfam:/// orientation markdown.
func renderRoot(d discoveryData) []byte {
	t := d.tmpl
	var b strings.Builder
	fmt.Fprintf(&b, "# botfam %s\n\n", serverVersion)
	b.WriteString("This MCP server is self-describing. A fresh agent can become operative from these resources alone.\n\n")

	b.WriteString("## This fam\n\n")
	fmt.Fprintf(&b, "- **fam**: %s\n", orPlaceholder(t.Fam, "<unresolved>"))
	fmt.Fprintf(&b, "- **actor**: %s\n", orPlaceholder(t.Actor, "<unresolved>"))
	fmt.Fprintf(&b, "- **main channel**: %s\n", orPlaceholder(t.MainChannel, "<unresolved>"))

	b.WriteString("\n## Start here\n\n")
	b.WriteString("- `botfam:///index.json` — this orientation as structured data\n")
	b.WriteString("- `botfam:///docs/start` — onboarding for a fresh agent\n")
	b.WriteString("- `botfam:///docs/protocol` — the coordination protocol\n")
	for _, slug := range discoverySlugs {
		if slug == "start" || slug == "protocol" {
			continue
		}
		fmt.Fprintf(&b, "- `botfam:///docs/%s`\n", slug)
	}

	b.WriteString("\n## Catalogs\n\n")
	b.WriteString("- `botfam:///tools` — human index of exposed tools\n")
	b.WriteString("- `botfam:///tools.json` — structured tools catalog\n")
	b.WriteString("- `botfam:///skills` — human index of repository skills\n")
	b.WriteString("- `botfam:///skills.json` — structured skills catalog\n")

	b.WriteString("\n## Health\n\n")
	allOK := true
	for _, h := range d.health {
		if h.Status == "ok" {
			fmt.Fprintf(&b, "- ✅ %s\n", h.Check)
		} else {
			allOK = false
			fmt.Fprintf(&b, "- ⚠️ %s — %s\n", h.Check, h.Fix)
		}
	}
	if allOK && len(d.health) > 0 {
		b.WriteString("\nAll checks pass.\n")
	}
	return []byte(b.String())
}

// discoveryIndex is the botfam.discovery.v1 JSON schema served at
// botfam:///index.json.
type discoveryIndex struct {
	Schema string `json:"schema"`
	Server struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"server"`
	Fam struct {
		Name        string `json:"name"`
		Actor       string `json:"actor"`
		MainChannel string `json:"main_channel"`
	} `json:"fam"`
	Resources []string      `json:"resources"`
	Health    []healthCheck `json:"health"`
}

func renderIndexJSON(d discoveryData) ([]byte, error) {
	var idx discoveryIndex
	idx.Schema = "botfam.discovery.v1"
	idx.Server.Name = serverName
	idx.Server.Version = serverVersion
	idx.Fam.Name = d.tmpl.Fam
	idx.Fam.Actor = d.tmpl.Actor
	idx.Fam.MainChannel = d.tmpl.MainChannel
	idx.Resources = []string{
		"botfam:///",
		"botfam:///index.json",
		"botfam:///tools",
		"botfam:///tools.json",
		"botfam:///skills",
		"botfam:///skills.json",
	}
	for _, slug := range discoverySlugs {
		idx.Resources = append(idx.Resources, "botfam:///docs/"+slug)
	}
	idx.Health = d.health
	return json.MarshalIndent(idx, "", "  ")
}

func orPlaceholder(v, ph string) string {
	if v == "" {
		return ph
	}
	return v
}

// isKnownSlug reports whether slug is a registered embedded doc.
func isKnownSlug(slug string) bool {
	for _, s := range discoverySlugs {
		if s == slug {
			return true
		}
	}
	return false
}

func (s *server) getTools() []mcplib.Tool {
	var tools []mcplib.Tool
	srv := s.mcpSrv
	if srv == nil {
		srv = mcpserver.NewMCPServer(serverName, serverVersion)
		s.registerTools(srv)
	}
	for _, st := range srv.ListTools() {
		tools = append(tools, st.Tool)
	}
	// Sort by name for deterministic ordering
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func toolDomain(name string) string {
	if strings.HasPrefix(name, "irc_") {
		return "irc"
	}
	if strings.HasPrefix(name, "worktree_") {
		return "worktree"
	}
	return "unknown"
}

func toolReadOnly(name string) bool {
	switch name {
	case "irc_read", "irc_wait":
		return true
	default:
		return false
	}
}

func renderToolsMarkdown(s *server) []byte {
	tools := s.getTools()

	// Group tools by domain
	byDomain := make(map[string][]mcplib.Tool)
	for _, t := range tools {
		dom := toolDomain(t.Name)
		byDomain[dom] = append(byDomain[dom], t)
	}

	var b strings.Builder
	b.WriteString("# botfam Tools Catalog\n\n")
	b.WriteString("This catalog lists the tools exposed by the botfam MCP server.\n\n")

	// Print domains in sorted order
	var domains []string
	for dom := range byDomain {
		domains = append(domains, dom)
	}
	sort.Strings(domains)

	for _, dom := range domains {
		fmt.Fprintf(&b, "## Domain: %s\n\n", dom)
		for _, t := range byDomain[dom] {
			roStr := "read-write"
			if toolReadOnly(t.Name) {
				roStr = "read-only"
			}
			fmt.Fprintf(&b, "- **%s** (%s) — %s\n", t.Name, roStr, t.Description)
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}

type toolEntry struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Domain          string `json:"domain"`
	ReadOnly        bool   `json:"read_only"`
	InputSchemaHash string `json:"input_schema_hash"`
}

type toolsCatalog struct {
	Schema string      `json:"schema"`
	Tools  []toolEntry `json:"tools"`
}

func (s *server) renderToolsJSON() ([]byte, error) {
	tools := s.getTools()
	var catalog toolsCatalog
	catalog.Schema = "botfam.tools.v1"

	for _, t := range tools {
		schemaBytes, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("marshal input schema for tool %q: %w", t.Name, err)
		}
		hash := sha256.Sum256(schemaBytes)
		hashHex := fmt.Sprintf("%x", hash)

		catalog.Tools = append(catalog.Tools, toolEntry{
			Name:            t.Name,
			Description:     t.Description,
			Domain:          toolDomain(t.Name),
			ReadOnly:        toolReadOnly(t.Name),
			InputSchemaHash: hashHex,
		})
	}

	return json.MarshalIndent(catalog, "", "  ")
}

func renderSkillsMarkdown(repoRoot string) ([]byte, error) {
	skills, err := fam.ReadRepoSkills(repoRoot)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("# botfam Skills Catalog\n\n")
	b.WriteString("Generated from `skills/*/SKILL.md`.\n\n")

	for _, s := range skills {
		fmt.Fprintf(&b, "- **[%s](botfam:///skills/%s)**: %s\n", s.Name, s.Name, s.Description)
	}
	return []byte(b.String()), nil
}

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

type skillsCatalog struct {
	Schema string       `json:"schema"`
	Skills []skillEntry `json:"skills"`
}

func renderSkillsJSON(repoRoot string) ([]byte, error) {
	skills, err := fam.ReadRepoSkills(repoRoot)
	if err != nil {
		return nil, err
	}

	catalog := skillsCatalog{
		Schema: "botfam.skills.v1",
	}
	for _, s := range skills {
		catalog.Skills = append(catalog.Skills, skillEntry{
			Name:        s.Name,
			Description: s.Description,
			Path:        s.Path,
		})
	}
	return json.MarshalIndent(catalog, "", "  ")
}

func readSkillMarkdown(repoRoot string, name string) ([]byte, error) {
	skills, err := fam.ReadRepoSkills(repoRoot)
	if err != nil {
		return nil, err
	}

	var foundPath string
	for _, s := range skills {
		if s.Name == name {
			foundPath = s.Path
			break
		}
	}
	if foundPath == "" {
		return nil, fmt.Errorf("skill %q not found in repository", name)
	}

	content, err := os.ReadFile(filepath.Join(repoRoot, foundPath))
	if err != nil {
		return nil, fmt.Errorf("read skill file: %w", err)
	}
	return content, nil
}
