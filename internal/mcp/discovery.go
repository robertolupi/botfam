package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

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
	"start", "protocol", "bootstrap", "ops", "operator", "review", "worktrees", "markdown",
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
	// resolvedVia records which tier of resolveDiscoveryWorkDir produced the
	// work dir (collab_root|cwd|roots|pwd|cwd_fallback|default|work_dir). It is
	// surfaced on the root + index so an agent sees how its home was found
	// instead of reverse-engineering the mount topology (#137; seeds botfam:trace).
	resolvedVia string
}

// buildDiscoveryData resolves the fam-specific runtime config for workDir. It
// never fails: unresolved fields stay empty (rendered as <placeholders>) and
// are surfaced as health warnings.
func buildDiscoveryData(workDir string) discoveryData {
	var d discoveryData

	var reg fam.Registry
	var harness string
	// Prefer the unified resolver: it reads the canonical <fam-dir>/fam.toml and
	// validates the worktree basename against the roster (the wt- prefix is
	// retired). It succeeds only in an [agent.<name>] worktree of a migrated fam.
	if rf, err := fam.ResolveFam(workDir); err == nil {
		d.tmpl.Actor = rf.Actor
		harness = rf.Agent.Harness
		reg = rf.Registry
		d.tmpl.Fam = rf.Slug
		if d.tmpl.Fam == "" {
			d.tmpl.Fam = rf.Name
		}
	} else {
		// Legacy / soft fallback: un-migrated fam, or a base/user worktree where
		// the runtime resolver fails closed. Surface what we can for display.
		if info, e := (fam.Resolver{WorkDir: workDir}).Resolve(); e == nil {
			d.tmpl.Actor = info.Actor
			d.tmpl.Fam = info.Name
		}
		reg = fam.LoadFamRegistry(workDir)
		if slug := fam.FamSlug(reg); slug != "" {
			d.tmpl.Fam = slug
		}
	}
	d.tmpl.MainChannel, d.tmpl.CcrepChannel = fam.FamChannels(reg)
	d.tmpl.IntegrationBranch = fam.FamBranch(reg)
	d.tmpl.ForgeURL = reg.Origin
	d.projections = wiki.ParseProjections(reg.WikiProjections)
	hasMemory := false
	for _, proj := range d.projections {
		if proj.Name == "memory" {
			hasMemory = true
			break
		}
	}
	if !hasMemory {
		d.projections = append(d.projections, wiki.Projection{
			Name:  "memory",
			Match: "memory-*",
		})
	}

	d.health = discoveryHealth(workDir, d.tmpl, harness)
	return d
}

// resolveDiscoveryWorkDir finds the work dir to resolve fam/actor for a
// resource read. Resources are param-less, and a system-wide MCP server runs
// with CWD=/, so this layered chain (first hit wins) makes the discovery root
// resolve across mount topologies (#132):
//  1. COLLAB_ROOT env (explicit)
//  2. server CWD, if it sits inside a fam (per-project mounts + the CLI path)
//  3. client workspace roots via the MCP `roots` capability (system-wide mounts)
//  4. the launching shell's PWD
//  5. server CWD as a last resort (surfaces <unresolved> + a health warning)
func (s *server) resolveDiscoveryWorkDir(ctx context.Context) string {
	dir, _ := s.resolveDiscoveryWorkDirVia(ctx)
	return dir
}

// rootsRequester fetches the client's workspace roots over the MCP `roots`
// capability. It is a seam so resolveWorkDir's tier-3 path is unit-testable
// without a live client session (#136).
type rootsRequester func(ctx context.Context) (*mcplib.ListRootsResult, error)

// resolveDiscoveryWorkDirVia is resolveDiscoveryWorkDir plus the label of the
// tier that produced the result, for the resolved_via observability field (#137).
// It binds the live inputs (env, CWD, MCP client) and delegates the tier logic
// to resolveWorkDir.
func (s *server) resolveDiscoveryWorkDirVia(ctx context.Context) (dir, via string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	var requestRoots rootsRequester
	if s.mcpSrv != nil {
		requestRoots = func(ctx context.Context) (*mcplib.ListRootsResult, error) {
			return s.mcpSrv.RequestRoots(ctx, mcplib.ListRootsRequest{})
		}
	}
	return resolveWorkDir(ctx, os.Getenv("COLLAB_ROOT"), cwd, os.Getenv("PWD"), requestRoots, famResolvable)
}

// resolveWorkDir is the pure tier chain behind resolveDiscoveryWorkDirVia (first
// hit wins), separated from the live inputs (env, CWD, MCP client, fam
// detection) so every tier — including the client `roots` path that only fires
// on a system-wide mount (cwd=="/") — is testable:
//  1. collabRoot (COLLAB_ROOT env, explicit)
//  2. cwd, if it is a real dir other than "/" (per-project mounts, the CLI, tests)
//  3. client workspace roots via requestRoots (system-wide mounts, cwd=="/")
//  4. pwd (the launching shell's PWD), if it sits inside a fam
//  5. cwd as a last resort (surfaces <unresolved> + a health warning)
//
// resolvable reports whether a candidate dir sits inside a fam (famResolvable in
// production); tiers 3 and 4 only accept candidates it approves.
func resolveWorkDir(ctx context.Context, collabRoot, cwd, pwd string, requestRoots rootsRequester, resolvable func(string) bool) (dir, via string) {
	if collabRoot != "" {
		return collabRoot, "collab_root"
	}
	// A real working dir is the caller's context. Only "/" (a system-wide
	// mount's CWD) is treated as "no context" so we fall through to client
	// roots / PWD.
	if cwd != "" && cwd != "/" {
		return cwd, "cwd"
	}
	if requestRoots != nil {
		if res, err := requestRoots(ctx); err == nil && res != nil {
			for _, root := range res.Roots {
				if p := fileURIToPath(root.URI); p != "" && resolvable(p) {
					return p, "roots"
				}
			}
		}
	}
	if pwd != "" && resolvable(pwd) {
		return pwd, "pwd"
	}
	if cwd != "" {
		return cwd, "cwd_fallback"
	}
	return ".", "default"
}

// famResolvable reports whether dir sits inside a fam (a fam.toml is reachable).
func famResolvable(dir string) bool {
	return fam.FamSlug(fam.LoadFamRegistry(dir)) != ""
}

// fileURIToPath turns a file:// root URI into a local path, or "" if not a
// file URI.
func fileURIToPath(uri string) string {
	if !strings.HasPrefix(uri, "file://") {
		return ""
	}
	p := strings.TrimPrefix(uri, "file://")
	if strings.HasPrefix(p, "/") {
		return p // file:///abs/path
	}
	if i := strings.Index(p, "/"); i >= 0 {
		return p[i:] // file://host/abs/path -> /abs/path
	}
	return ""
}

func discoveryHealth(workDir string, t docs.TemplateData, harness string) []healthCheck {
	var checks []healthCheck

	if t.Actor == "" {
		checks = append(checks, healthCheck{"actor", "warn",
			"could not resolve an actor: run from an [agent.<name>] worktree (or set COLLAB_ACTOR)"})
	} else {
		checks = append(checks, healthCheck{"actor", "ok", ""})
	}

	// Forge token: the canonical per-harness token (~/.botfam/token-<harness>),
	// the same path the forge client + MCP actually use (forge.HarnessTokenPath)
	// — no legacy fallback, so this can't report ok on a token the MCP won't use
	// (#183).
	if t.Actor != "" && harness != "" {
		if tokenPath, err := forge.HarnessTokenPath(harness); err == nil {
			if fileExists(tokenPath) {
				checks = append(checks, healthCheck{"forge_token", "ok", ""})
			} else {
				checks = append(checks, healthCheck{"forge_token", "warn",
					fmt.Sprintf("no forge token at %s: mint it with `botfam mint --harness %s --user <forge-user>`", tokenPath, harness)})
			}
		}
	}

	// IRC client: check that a live client backs the FIFO by verifying the pidfile.
	if t.Actor != "" {
		fifo := filepath.Join(workDir, "scratch", "irc", t.Actor, "in")
		pidFile := filepath.Join(workDir, "scratch", "irc", t.Actor, "pid")
		clientRunning := false
		if fileExists(fifo) && fileExists(pidFile) {
			if pidData, err := os.ReadFile(pidFile); err == nil {
				var pid int
				if _, err := fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &pid); err == nil && pid > 0 {
					if processExists(pid) {
						clientRunning = true
					}
				}
			}
		}
		if clientRunning {
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

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if err == syscall.ESRCH {
		return false
	}
	return true
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
	if d.resolvedVia != "" {
		fmt.Fprintf(&b, "- **resolved via**: %s\n", d.resolvedVia)
	}

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

	b.WriteString("\n## Projections\n\n")
	if len(d.projections) > 0 {
		for _, proj := range d.projections {
			fmt.Fprintf(&b, "- `botfam:///%s` — live wiki projection: %s\n", proj.Name, proj.Name)
		}
	} else {
		b.WriteString("_No projections configured._\n")
	}

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
	ResolvedVia string        `json:"resolved_via,omitempty"`
	Resources   []string      `json:"resources"`
	Health      []healthCheck `json:"health"`
}

func renderIndexJSON(d discoveryData) ([]byte, error) {
	var idx discoveryIndex
	idx.Schema = "botfam.discovery.v1"
	idx.Server.Name = serverName
	idx.Server.Version = serverVersion
	idx.Fam.Name = d.tmpl.Fam
	idx.Fam.Actor = d.tmpl.Actor
	idx.Fam.MainChannel = d.tmpl.MainChannel
	idx.ResolvedVia = d.resolvedVia
	idx.Resources = []string{
		"botfam:///",
		"botfam:///index.json",
		"botfam:///problem",
		"botfam:///tools",
		"botfam:///tools.json",
		"botfam:///skills",
		"botfam:///skills.json",
	}
	for _, slug := range discoverySlugs {
		idx.Resources = append(idx.Resources, "botfam:///docs/"+slug)
	}
	for _, proj := range d.projections {
		idx.Resources = append(idx.Resources, "botfam:///"+proj.Name)
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
