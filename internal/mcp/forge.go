package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	contractconnect "github.com/robertolupi/botfam/internal/eventdelivery/contract/connect"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"github.com/robertolupi/botfam/internal/eventdelivery/workerchannel"
	"github.com/robertolupi/botfam/internal/famctx"

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

// addForgeEntries mounts every gitea-mcp tool into the dispatch table under a
// forge_ name, wrapping its handler so it runs through botfam's shared preamble
// (identity, fail-closed serve gate, cross-actor rule) with the resolved actor's
// forge token injected. This is the in-process replacement for the separate
// gitea-mcp-server process (#429), and the path a CattleSeam interceptor (#425)
// wraps along with every other tool. A tool's read-only annotation is carried
// onto the entry, so read tools are cross-actor-safe and writes are blocked
// cross-actor like any mutating tool (#427).
func addForgeEntries(entries map[string]dispatchEntry) {
	for _, dom := range forgeDomains() {
		for _, st := range dom.Tools() {
			tool := st.Tool
			origName := tool.Name
			tool.Name = forgeToolPrefix + origName
			ro := tool.Annotations.ReadOnlyHint
			h := st.Handler
			// The wiki tools need transparent handling of Gitea's ".-" slug
			// escaping; gitea-mcp upstream lacks it, so botfam wraps it here
			// rather than forking gitea-mcp (#464). See wikiSlugFallback.
			if origName == wiki.WikiReadToolName || origName == wiki.WikiWriteToolName {
				h = wikiSlugFallback(h)
			}
			entries[tool.Name] = dispatchEntry{
				tool:     tool,
				handler:  forgeHandler(tool.Name, origName, h, ro != nil && *ro),
				readOnly: ro != nil && *ro,
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
func forgeHandler(toolName, origName string, h func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error), readOnly bool) toolHandler {
	return func(ctx context.Context, rc *resolvedCtx, args map[string]any) (*mcplib.CallToolResult, error) {
		if !readOnly {
			if result, proxied, err := proxyForgeAction(ctx, rc, toolName, args); proxied || err != nil {
				return result, err
			}
		}

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

func proxyForgeAction(ctx context.Context, rc *resolvedCtx, toolName string, args map[string]any) (*mcplib.CallToolResult, bool, error) {
	endpoint, ok, err := resolveWorkerChannelEndpoint(ctx, rc)
	if err != nil || !ok {
		return nil, ok, err
	}
	workItemID := stringArg(args, "work_item_id")
	if workItemID == "" {
		workItemID = os.Getenv("BOTFAM_WORK_ITEM_ID")
	}
	if workItemID == "" {
		return nil, true, fmt.Errorf("worker proxy mode is active but no work_item_id was provided")
	}
	actionKey := stringArg(args, "action_key")
	if actionKey == "" {
		actionKey = deriveActionKey(toolName, args)
	}
	argumentsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, true, err
	}
	client := contractconnect.NewWorkerChannelClient(singlehost.UnixHTTPClient(endpoint.socketPath), "http://eventdelivery")
	req := connect.NewRequest(&pb.ForgeAction{
		WorkItemId:    workItemID,
		ActionKey:     actionKey,
		ToolName:      toolName,
		ArgumentsJson: string(argumentsJSON),
	})
	if endpoint.token != "" {
		req.Header().Set("Authorization", "Bearer "+endpoint.token)
	}
	if endpoint.fencingToken != 0 {
		req.Header().Set(workerchannel.FencingTokenHeader, strconv.FormatUint(endpoint.fencingToken, 10))
	}
	resp, err := client.ProposeForgeAction(ctx, req)
	if err != nil {
		return nil, true, err
	}
	return forgeActionToolResult(resp.Msg)
}

func forgeActionToolResult(ack *pb.ActionAck) (*mcplib.CallToolResult, bool, error) {
	if ack == nil {
		return nil, true, errors.New("worker channel returned empty forge action ack")
	}
	if !ack.GetCommitted() {
		return nil, true, fmt.Errorf("worker channel did not commit forge action outbox_id=%q deduped=%t", ack.GetOutboxId(), ack.GetDeduped())
	}
	return mcplib.NewToolResultText(ack.GetResponseJson()), true, nil
}

type workerEndpoint struct {
	socketPath   string
	token        string
	fencingToken uint64
}

func resolveWorkerChannelEndpoint(ctx context.Context, rc *resolvedCtx) (workerEndpoint, bool, error) {
	if socket := os.Getenv("BOTFAM_WORKER_CHANNEL_SOCKET"); socket != "" {
		token, _ := strconv.ParseUint(os.Getenv("BOTFAM_FENCING_TOKEN"), 10, 64)
		return workerEndpoint{socketPath: socket, fencingToken: token}, true, nil
	}
	resolverSocket := firstNonEmptyEnv("BOTFAM_SESSION_RESOLVER_SOCKET", "BOTFAM_EVENTDELIVERY_RESOLVER_SOCKET")
	if resolverSocket == "" {
		return workerEndpoint{}, false, nil
	}
	owner, repo, _ := strings.Cut(rc.c.Registry.Repository, "/")
	scope := &pb.Scope{RepoOwner: owner, RepoName: repo}
	if milestone := os.Getenv("BOTFAM_SCOPE_MILESTONE_ID"); milestone != "" {
		if id, err := strconv.ParseInt(milestone, 10, 64); err == nil {
			scope.MilestoneId = id
		}
	}
	client := contractconnect.NewSessionResolverClient(singlehost.UnixHTTPClient(resolverSocket), "http://eventdelivery")
	resp, err := client.Resolve(ctx, connect.NewRequest(scope))
	if err != nil {
		return workerEndpoint{}, true, err
	}
	if !resp.Msg.GetFound() {
		return workerEndpoint{}, false, nil
	}
	socketPath := workerSocketPath(resp.Msg.GetAddress())
	if socketPath == "" {
		return workerEndpoint{}, true, fmt.Errorf("session resolver returned live session %q without a unix worker address", resp.Msg.GetSessionId())
	}
	return workerEndpoint{socketPath: socketPath, token: resp.Msg.GetToken(), fencingToken: resp.Msg.GetFencingToken()}, true, nil
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func deriveActionKey(toolName string, args map[string]any) string {
	b, _ := json.Marshal(args)
	sum := sha256.Sum256(append([]byte(toolName+"\n"), b...))
	return "mcp:" + hex.EncodeToString(sum[:16])
}

func workerSocketPath(address string) string {
	address = strings.TrimSpace(address)
	for _, prefix := range []string{"unix://", "unix:"} {
		if strings.HasPrefix(address, prefix) {
			return strings.TrimPrefix(address, prefix)
		}
	}
	return address
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

// ForgeExecutor implements workerchannel.ForgeExecutor using the Gitea MCP tool
// handlers registered in this package.
type ForgeExecutor struct {
	rc *resolvedCtx
}

func NewForgeExecutor(fctx famctx.Context) *ForgeExecutor {
	actor := fctx.Slug
	if actor == "" {
		actor = "supervisor"
	}
	return &ForgeExecutor{
		rc: &resolvedCtx{
			workDir: fctx.WorkDir,
			actor:   actor,
			c:       fctx,
		},
	}
}

func (e *ForgeExecutor) ExecuteForgeAction(ctx context.Context, toolName, argumentsJSON string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return "", fmt.Errorf("unmarshal arguments: %w", err)
	}

	entries := make(map[string]dispatchEntry)
	addForgeEntries(entries)

	entry, ok := entries[toolName]
	if !ok {
		return "", fmt.Errorf("unsupported forge tool: %q", toolName)
	}

	res, err := entry.handler(ctx, e.rc, args)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), nil
}

var _ workerchannel.ForgeExecutor = (*ForgeExecutor)(nil)

