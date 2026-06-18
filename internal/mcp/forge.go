package mcp

import (
	"context"
	"os"
	"strings"
	"sync"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"gitea.com/gitea/gitea-mcp/operation/actions"
	"gitea.com/gitea/gitea-mcp/operation/issue"
	"gitea.com/gitea/gitea-mcp/operation/label"
	"gitea.com/gitea/gitea-mcp/operation/milestone"
	"gitea.com/gitea/gitea-mcp/operation/notification"
	"gitea.com/gitea/gitea-mcp/operation/packages"
	"gitea.com/gitea/gitea-mcp/operation/pull"
	"gitea.com/gitea/gitea-mcp/operation/repo"
	"gitea.com/gitea/gitea-mcp/operation/search"
	"gitea.com/gitea/gitea-mcp/operation/timetracking"
	"gitea.com/gitea/gitea-mcp/operation/user"
	"gitea.com/gitea/gitea-mcp/operation/version"
	"gitea.com/gitea/gitea-mcp/operation/wiki"
	giteactx "gitea.com/gitea/gitea-mcp/pkg/context"
	giteaflag "gitea.com/gitea/gitea-mcp/pkg/flag"
	giteatool "gitea.com/gitea/gitea-mcp/pkg/tool"
)

// forgeToolPrefix namespaces the in-process gitea-mcp tools so they never
// collide with botfam's own surface (irc_*, worktree_*, orient). The model sees
// e.g. mcp__botfam__forge_issue_read. See the pinned naming decision on #429.
const forgeToolPrefix = "forge_"

// forgeFlagOnce sets the process-global gitea-mcp host exactly once, from the
// first resolved fam. botfam serves one worktree (hence one forge) per process,
// so the host is effectively constant; the per-actor token is the only per-call
// value and rides the request context (see forgeHandler).
var forgeFlagOnce sync.Once

// forgeDomains are the gitea-mcp operation registries mounted as botfam subtools.
// Mirrors operation.domainTools upstream.
func forgeDomains() []*giteatool.Tool {
	return []*giteatool.Tool{
		issue.Tool, pull.Tool, repo.Tool, label.Tool, milestone.Tool,
		notification.Tool, packages.Tool, search.Tool, user.Tool,
		version.Tool, wiki.Tool, timetracking.Tool, actions.Tool,
	}
}

// init marks the read-only forge tools as cross-actor-safe, keyed by their
// prefixed name. It runs after the imported operation packages' own init()
// (which register their tools), so the registries are populated here. Doing it
// in init (single goroutine, before serving) keeps buildEntries free of global
// mutation. Write tools are omitted, so a forge write from another agent's
// worktree is blocked by the same cross-actor rule as any mutating tool.
func init() {
	for _, dom := range forgeDomains() {
		for _, st := range dom.Tools() {
			if h := st.Tool.Annotations.ReadOnlyHint; h != nil && *h {
				readOnlyTools[forgeToolPrefix+st.Tool.Name] = true
			}
		}
	}
}

// addForgeEntries mounts every gitea-mcp tool into the dispatch table under a
// forge_ name, wrapping its handler so it runs through botfam's shared preamble
// (identity, fail-closed serve gate, cross-actor rule) with the resolved actor's
// forge token injected. This is the in-process replacement for the separate
// gitea-mcp-server process (#429), and the path a CattleSeam interceptor (#425)
// wraps along with every other tool.
func addForgeEntries(entries map[string]dispatchEntry) {
	for _, dom := range forgeDomains() {
		for _, st := range dom.Tools() {
			tool := st.Tool
			origName := tool.Name
			tool.Name = forgeToolPrefix + origName
			entries[tool.Name] = dispatchEntry{
				tool:    tool,
				handler: forgeHandler(origName, st.Handler),
			}
		}
	}
}

// forgeHandler adapts a gitea-mcp tool handler to botfam's dispatch signature:
// it sets the forge host once, injects the resolved actor's per-harness token
// (~/.botfam/token-<harness>, already resolved into c.TokenPath) into the
// context that pkg/gitea.ClientFromContext reads, then invokes the original
// handler. A missing token is left to surface as a forge auth error rather than
// failing here, so the tool still reports a clear, actionable result.
func forgeHandler(origName string, h func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error)) toolHandler {
	return func(ctx context.Context, rc *resolvedCtx, args map[string]any) (*mcplib.CallToolResult, error) {
		forgeFlagOnce.Do(func() {
			if host := rc.c.Registry.ForgeURL; host != "" {
				giteaflag.Host = host
			} else if rc.c.Registry.Origin != "" {
				giteaflag.Host = rc.c.Registry.Origin
			}
			giteaflag.Version = serverVersion
		})

		if rc.c.TokenPath != "" {
			if b, err := os.ReadFile(rc.c.TokenPath); err == nil {
				ctx = context.WithValue(ctx, giteactx.TokenContextKey, strings.TrimSpace(string(b)))
			}
		}

		var fr mcplib.CallToolRequest
		fr.Params.Name = origName
		fr.Params.Arguments = args
		return h(ctx, fr)
	}
}
