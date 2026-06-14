package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/robertolupi/botfam/internal/docs"
	"github.com/robertolupi/botfam/internal/fam"
	"github.com/robertolupi/botfam/internal/wiki"
)

// serverName/serverVersion identify this MCP server in its handshake and in
// the self-discovery resources (botfam:/// and index.json).
const (
	serverName    = "botfam"
	serverVersion = "0.1.0"
)

// errIdentityRequired signals that no actor identity could be resolved from
// any source (call arg, bound session, env, worktree directory).
var errIdentityRequired = errors.New("identity required: pass actor, set COLLAB_ACTOR, or run from a named worktree")

// identityOptionalTools are tools whose handlers never use the calling actor.
// For these, a missing identity is tolerated; identity conflicts are still
// rejected and a resolved identity still binds the session as usual.
var identityOptionalTools = map[string]bool{
	"worktree_init": true,
	"worktree_sync": true,
}

type server struct {
	envActor string
	lockMode bool
	mcpSrv   *mcpserver.MCPServer

	mu    sync.Mutex
	actor string
}

func Serve(in io.Reader, out io.Writer, errout io.Writer) error {
	s := &server{
		envActor: os.Getenv("COLLAB_ACTOR"),
		lockMode: lockActorEnabled(),
	}
	mcpSrv := mcpserver.NewMCPServer(serverName, serverVersion, mcpserver.WithToolCapabilities(false), mcpserver.WithRoots())
	s.mcpSrv = mcpSrv
	s.registerTools(mcpSrv)
	s.registerResources(mcpSrv)
	return serveStdio(context.Background(), mcpSrv, in, out)
}

func (s *server) registerTools(mcpSrv *mcpserver.MCPServer) {
	add := func(tool mcplib.Tool) {
		mcpSrv.AddTool(tool, func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return s.callTool(ctx, req.Params.Name, req.GetArguments())
		})
	}

	add(mcplib.NewTool("irc_write",
		mcplib.WithDescription("Write a raw line to the IRC client's input pipe."),
		mcplib.WithString("message", mcplib.Required()),
		mcplib.WithString("target"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("irc_read",
		mcplib.WithDescription("Read lines from the IRC client's log (raw tail, no filtering)."),
		mcplib.WithNumber("lines"),
		mcplib.WithNumber("from_offset"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("irc_wait",
		mcplib.WithDescription("Block until new IRC log lines relevant to the actor appear, or timeout."),
		mcplib.WithNumber("timeout_s"),
		mcplib.WithNumber("from_offset"),
		mcplib.WithString("actor"),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("worktree_init",
		mcplib.WithDescription("Initialize git worktree configuration and identity for an actor."),
		mcplib.WithString("target_actor", mcplib.Required()),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("worktree_sync",
		mcplib.WithDescription("Safely bring the worktree up to date with main (auto-stash, merge main, pop stash)."),
		mcplib.WithString("work_dir"),
	))
	add(mcplib.NewTool("orient",
		mcplib.WithDescription("Return this fam's discovery root (fam, actor, health, channels) as botfam.discovery.v1 JSON, resolved from work_dir. Use this when botfam:/// shows <unresolved> (e.g. a system-wide MCP mount). Defaults work_dir to $PWD."),
		mcplib.WithString("work_dir"),
	))
}

func (s *server) callTool(ctx context.Context, name string, args map[string]any) (*mcplib.CallToolResult, error) {
	// orient is a read-only identity/orientation probe: it bypasses the
	// membership/identity preamble and resolves the discovery root for the
	// given work_dir (defaulting to $PWD). This is the authoritative path on
	// system-wide mounts where the param-less botfam:/// resource can't see the
	// caller's worktree (#132).
	if name == "orient" {
		wd := argString(args, "work_dir")
		if wd == "" {
			wd = os.Getenv("PWD")
		}
		if wd == "" {
			wd = "."
		}
		body, err := renderIndexJSON(buildDiscoveryData(wd))
		if err != nil {
			return nil, err
		}
		return mcplib.NewToolResultText(string(body)), nil
	}

	workDir := argString(args, "work_dir")
	if workDir == "" {
		workDir = "."
	}
	info, err := (fam.Resolver{WorkDir: workDir, Env: os.Environ()}).Resolve()
	if err != nil {
		return nil, err
	}
	if err := fam.EnsureMembership(info.Root, info.Explicit, workDir); err != nil {
		return nil, err
	}

	actor, err := s.resolveActor(argString(args, "actor"), info.Actor)
	if err != nil {
		if !identityOptionalTools[name] || !errors.Is(err, errIdentityRequired) {
			return nil, err
		}
		actor = ""
	}

	if name == "worktree_init" {
		targetActor := argString(args, "target_actor")
		if targetActor == "" {
			return nil, errors.New("target_actor is required")
		}
		var buf bytes.Buffer
		err := fam.WorktreeCmd([]string{"init", targetActor, workDir}, &buf)
		if err != nil {
			return nil, err
		}
		return toolResult(map[string]any{"ok": true, "output": buf.String()})
	}

	if name == "worktree_sync" {
		var buf bytes.Buffer
		err := fam.WorktreeCmd([]string{"sync", workDir}, &buf)
		if err != nil {
			return nil, err
		}
		return toolResult(map[string]any{"ok": true, "output": buf.String()})
	}

	if name == "irc_write" {
		message := argString(args, "message")
		if message == "" {
			return nil, errors.New("message is required")
		}
		target := argString(args, "target")

		absWorkDir, err := filepath.Abs(workDir)
		if err != nil {
			return nil, err
		}

		fifoPath := filepath.Join(absWorkDir, "scratch", "irc", actor, "in")
		fi, err := os.Stat(fifoPath)
		if err != nil {
			return nil, fmt.Errorf("IRC FIFO not found at %s: %w", fifoPath, err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			return nil, fmt.Errorf("path %s is not a named pipe", fifoPath)
		}

		f, err := os.OpenFile(fifoPath, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if err != nil {
			return nil, fmt.Errorf("failed to open IRC FIFO (is the client running?): %w", err)
		}
		defer f.Close()

		msg := message
		if target != "" {
			msg = fmt.Sprintf("/msg %s %s", target, message)
		}
		if !strings.HasSuffix(msg, "\n") {
			msg += "\n"
		}

		if _, err := f.WriteString(msg); err != nil {
			return nil, fmt.Errorf("failed to write to IRC FIFO: %w", err)
		}

		return toolResult(map[string]any{"ok": true})
	}

	if name == "irc_read" {
		absWorkDir, err := filepath.Abs(workDir)
		if err != nil {
			return nil, err
		}

		logPath := filepath.Join(absWorkDir, "scratch", "irc", actor, "log")
		if _, err := os.Stat(logPath); err != nil {
			return nil, fmt.Errorf("IRC log not found at %s (is the client running?): %w", logPath, err)
		}

		maxLines := int(argFloatDefault(args, "lines", 0))
		fromOffset := int64(argFloatDefault(args, "from_offset", -1))
		lines, nextOffset, err := fam.ReadIrcLog(logPath, fromOffset, maxLines)
		if err != nil {
			return nil, err
		}
		if lines == nil {
			lines = []string{}
		}
		return toolResult(map[string]any{"lines": lines, "next_offset": nextOffset})
	}

	if name == "irc_wait" {
		absWorkDir, err := filepath.Abs(workDir)
		if err != nil {
			return nil, err
		}

		logPath := filepath.Join(absWorkDir, "scratch", "irc", actor, "log")
		timeoutS := argFloatDefault(args, "timeout_s", 60)
		if timeoutS <= 0 {
			timeoutS = 60
		}
		if timeoutS > 300 {
			timeoutS = 300
		}
		fromOffset := int64(argFloatDefault(args, "from_offset", -1))
		lines, nextOffset, timedOut, err := fam.WaitIrcLines(logPath, actor, fromOffset, time.Duration(timeoutS*float64(time.Second)))
		if err != nil {
			return nil, err
		}
		if lines == nil {
			lines = []string{}
		}
		return toolResult(map[string]any{"lines": lines, "next_offset": nextOffset, "timed_out": timedOut})
	}

	return nil, fmt.Errorf("unknown tool %q", name)
}

func (s *server) resolveActor(callActor string, dirActor string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dirActor != "" {
		if callActor != "" && callActor != dirActor {
			return "", fmt.Errorf("actor %q conflicts with resolved directory actor %q", callActor, dirActor)
		}
		if s.envActor != "" && s.envActor != dirActor {
			return "", fmt.Errorf("COLLAB_ACTOR %q conflicts with resolved directory actor %q", s.envActor, dirActor)
		}
		if s.actor != "" && s.actor != dirActor {
			return "", fmt.Errorf("bound session actor %q conflicts with resolved directory actor %q", s.actor, dirActor)
		}
	}
	if s.lockMode {
		if s.envActor == "" {
			return "", errors.New("BOTFAM_LOCK_ACTOR is set but COLLAB_ACTOR is empty")
		}
		if callActor != "" && callActor != s.envActor {
			return "", fmt.Errorf("actor %q conflicts with locked COLLAB_ACTOR %q", callActor, s.envActor)
		}
		if s.actor == "" {
			s.actor = s.envActor
		}
		return s.actor, nil
	}
	candidate := callActor
	if candidate == "" {
		candidate = s.actor
	}
	if candidate == "" {
		candidate = s.envActor
	}
	if candidate == "" {
		candidate = dirActor
	}
	if candidate == "" {
		return "", errIdentityRequired
	}
	if err := validateActorName(candidate); err != nil {
		return "", err
	}
	if s.actor == "" {
		s.actor = candidate
		return candidate, nil
	}
	if callActor != "" && callActor != s.actor {
		return "", fmt.Errorf("actor %q conflicts with bound session actor %q", callActor, s.actor)
	}
	return s.actor, nil
}

func validateActorName(name string) error {
	if name == "" {
		return errors.New("actor name cannot be empty")
	}
	for _, r := range name {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return fmt.Errorf("invalid actor %q: must match [A-Za-z0-9_-]+", name)
		}
	}
	return nil
}

func toolResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcplib.NewToolResultText(string(b)), nil
}

// serveStdio implements the MCP stdio transport: messages are newline-delimited
// JSON, one per line, with no framing headers. The reader (readFrame) also
// tolerates legacy Content-Length-framed input, but responses are always written
// as a single line of JSON terminated by '\n'.
func serveStdio(ctx context.Context, mcpSrv *mcpserver.MCPServer, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	var writeMu sync.Mutex
	for {
		body, err := readFrame(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		resp := mcpSrv.HandleMessage(ctx, body)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		b = append(b, '\n')
		writeMu.Lock()
		_, err = out.Write(b)
		writeMu.Unlock()
		if err != nil {
			return err
		}
	}
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return []byte(trimmed), nil
		}

		contentLen := 0
		for {
			k, v, ok := strings.Cut(trimmed, ":")
			if ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return nil, err
				}
				contentLen = n
			}
			line, err = r.ReadString('\n')
			if err != nil {
				return nil, err
			}
			trimmed = strings.TrimSpace(line)
			if trimmed == "" {
				break
			}
		}
		if contentLen <= 0 {
			return nil, errors.New("missing Content-Length")
		}
		body := make([]byte, contentLen)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		return body, nil
	}
}

func lockActorEnabled() bool {
	if os.Getenv("BOTFAM_LOCK_ACTOR") == "1" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "botfam", "config")
	if os.Getenv("XDG_CONFIG_HOME") == "" {
		path = filepath.Join(home, ".config", "botfam", "config")
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "lock_actor = true")
}

func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func argFloatDefault(args map[string]any, key string, def float64) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return def
	}
}

func (s *server) registerResources(mcpSrv *mcpserver.MCPServer) {
	add := func(uri, name, mime string) {
		mcpSrv.AddResource(mcplib.NewResource(uri, name, mcplib.WithMIMEType(mime)), s.handleReadResource)
	}
	// One discovery root, plus its structured form.
	add("botfam:///", "botfam discovery root", "text/markdown")
	add("botfam:///index.json", "botfam discovery index", "application/json")
	// The embedded generic docs corpus (#117). Served from the binary, so
	// these work in a repo with no local doc/ checked in.
	for _, slug := range discoverySlugs {
		add("botfam:///docs/"+slug, "botfam doc: "+slug, "text/markdown")
	}
	// Phase 2: tools & skills catalogs
	add("botfam:///tools", "botfam tools catalog", "text/markdown")
	add("botfam:///tools.json", "botfam tools catalog", "application/json")
	add("botfam:///skills", "botfam skills catalog", "text/markdown")
	add("botfam:///skills.json", "botfam skills catalog", "application/json")

	// Resource template for individual skills
	mcpSrv.AddResourceTemplate(mcplib.NewResourceTemplate("botfam:///skills/{name}", "botfam skill document"), s.handleReadResource)

	// Live forge wiki (#119). Individual pages (botfam:///wiki/<page>) are
	// discovered via the index rather than statically advertised.
	add("botfam:///wiki", "botfam live wiki index", "text/markdown")
	add("botfam:///wiki/index.json", "botfam live wiki index (json)", "application/json")
	// Fam-declared wiki projections (#120), advertised from the local registry.
	for _, proj := range buildDiscoveryData(".").projections {
		add("botfam:///"+proj.Name, "botfam projection: "+proj.Name, "text/markdown")
		add("botfam:///"+proj.Name+".json", "botfam projection: "+proj.Name, "application/json")
	}
}

func (s *server) handleReadResource(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	cwd := s.resolveDiscoveryWorkDir(ctx)
	localRepoRoot := fam.RepoPath(cwd)

	u, err := url.Parse(req.Params.URI)
	if err != nil {
		return nil, fmt.Errorf("invalid resource URI %q: %w", req.Params.URI, err)
	}

	if u.Scheme != "botfam" {
		return nil, fmt.Errorf("unsupported scheme %q (expected \"botfam\")", u.Scheme)
	}

	// Resolve target repository root based on authority (Host)
	var targetRepoRoot string
	if u.Host == "" {
		targetRepoRoot = localRepoRoot
	} else {
		// Named authority. Resolve the local family first so a name/slug that
		// refers to this fam never scans ~/.botfam.
		localInfo, errInfo := (fam.Resolver{WorkDir: cwd}).Resolve()
		localReg := fam.LoadFamRegistry(cwd)
		if (errInfo == nil && u.Host == localInfo.Name) || u.Host == localReg.Name || u.Host == localReg.Slug {
			targetRepoRoot = localRepoRoot
		} else {
			// Cross-fam: search ~/.botfam/ for a family matching name or slug.
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, err
			}
			botfamDir := filepath.Join(home, ".botfam")
			entries, err := os.ReadDir(botfamDir)
			if err != nil {
				return nil, fmt.Errorf("failed to read ~/.botfam: %w", err)
			}
			found := false
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				tomlPath := filepath.Join(botfamDir, entry.Name(), "fam.toml")
				if _, err := os.Stat(tomlPath); err == nil {
					reg, err := fam.ReadRegistry(tomlPath)
					if err == nil {
						if reg.Name == u.Host || reg.Slug == u.Host {
							if len(reg.RepoPaths) > 0 {
								targetRepoRoot = reg.RepoPaths[0]
								found = true
								break
							}
						}
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown family authority %q", u.Host)
			}
		}
	}

	// Build the fam-specific runtime context. Local (and local-named) reads use
	// cwd; a resolved cross-fam authority uses that fam's repo root.
	dataWorkDir := cwd
	if targetRepoRoot != localRepoRoot {
		dataWorkDir = targetRepoRoot
	}
	d := buildDiscoveryData(dataWorkDir)

	path := filepath.Clean(u.Path)
	if u.Path == "" || path == "." {
		path = "/"
	}

	switch {
	case path == "/":
		return markdownResource(req.Params.URI, renderRoot(d)), nil
	case path == "/index.json":
		body, err := renderIndexJSON(d)
		if err != nil {
			return nil, err
		}
		return []mcplib.ResourceContents{mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(body),
		}}, nil
	case path == "/tools":
		return markdownResource(req.Params.URI, renderToolsMarkdown(s)), nil
	case path == "/tools.json":
		body, err := s.renderToolsJSON()
		if err != nil {
			return nil, err
		}
		return []mcplib.ResourceContents{mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(body),
		}}, nil
	case path == "/skills":
		body, err := renderSkillsMarkdown(dataWorkDir)
		if err != nil {
			return nil, err
		}
		return markdownResource(req.Params.URI, body), nil
	case path == "/skills.json":
		body, err := renderSkillsJSON(dataWorkDir)
		if err != nil {
			return nil, err
		}
		return []mcplib.ResourceContents{mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(body),
		}}, nil
	case strings.HasPrefix(path, "/skills/"):
		skillName := strings.TrimPrefix(path, "/skills/")
		body, err := readSkillMarkdown(dataWorkDir, skillName)
		if err != nil {
			return nil, err
		}
		return markdownResource(req.Params.URI, body), nil
	case strings.HasPrefix(path, "/docs/"):
		// Embedded generic corpus (#117): served from the binary, never the
		// local checkout. Unknown slugs fail closed with the known set.
		slug := strings.TrimPrefix(path, "/docs/")
		if !isKnownSlug(slug) {
			return nil, fmt.Errorf("unknown doc %q (known: %s)", slug, strings.Join(discoverySlugs, ", "))
		}
		content, err := docs.Render(slug, d.tmpl)
		if err != nil {
			return nil, fmt.Errorf("render doc %q: %w", slug, err)
		}
		return markdownResource(req.Params.URI, content), nil
	case path == "/wiki" || path == "/wiki/index.json":
		// Live forge wiki index (#119): forge API, else flagged-stale cache.
		prov, err := wikiProvider(dataWorkDir, d.tmpl.Actor)
		if err != nil {
			return nil, err
		}
		metas, err := prov.Index()
		if err != nil {
			return nil, fmt.Errorf("wiki index: %w", err)
		}
		if path == "/wiki/index.json" {
			body, err := renderWikiIndexJSON(metas, prov.Source())
			if err != nil {
				return nil, err
			}
			return []mcplib.ResourceContents{mcplib.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(body),
			}}, nil
		}
		return markdownResource(req.Params.URI, renderWikiIndexMarkdown(metas, prov.Source())), nil
	case strings.HasPrefix(path, "/wiki/"):
		// A single live wiki page. Name is constrained to the forge wiki
		// namespace; traversal/arbitrary reads are rejected by the provider.
		name := strings.TrimPrefix(path, "/wiki/")
		if !wiki.ValidPageName(name) {
			return nil, fmt.Errorf("invalid wiki page %q", name)
		}
		prov, err := wikiProvider(dataWorkDir, d.tmpl.Actor)
		if err != nil {
			return nil, err
		}
		page, err := prov.Page(name)
		if err != nil {
			return nil, fmt.Errorf("wiki page %q: %w", name, err)
		}
		return markdownResource(req.Params.URI, renderWikiPage(page)), nil
	default:
		// A fam-declared wiki projection? botfam:///<name> or <name>.json (#120).
		pname := strings.TrimPrefix(path, "/")
		wantJSON := strings.HasSuffix(pname, ".json")
		pname = strings.TrimSuffix(pname, ".json")
		for _, proj := range d.projections {
			if proj.Name != pname {
				continue
			}
			prov, err := wikiProvider(dataWorkDir, d.tmpl.Actor)
			if err != nil {
				return nil, err
			}
			idx, err := prov.Index()
			if err != nil {
				return nil, fmt.Errorf("projection %q: %w", proj.Name, err)
			}
			metas := wiki.Filter(idx, proj.Match)
			if wantJSON {
				body, err := renderProjectionJSON(proj.Name, proj.Match, prov.Source(), metas)
				if err != nil {
					return nil, err
				}
				return []mcplib.ResourceContents{mcplib.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(body),
				}}, nil
			}
			return markdownResource(req.Params.URI, renderProjectionMarkdown(proj.Name, proj.Match, prov.Source(), metas)), nil
		}
		return nil, fmt.Errorf("unknown resource path %q", u.Path)
	}
}
