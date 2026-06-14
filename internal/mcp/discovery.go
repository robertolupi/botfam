package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/robertolupi/botfam/internal/docs"
	"github.com/robertolupi/botfam/internal/fam"
)

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
	tmpl   docs.TemplateData
	health []healthCheck
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
	idx.Resources = []string{"botfam:///", "botfam:///index.json"}
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
