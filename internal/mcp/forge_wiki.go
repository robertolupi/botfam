package mcp

import (
	"context"
	"encoding/json"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// giteaToolHandler is the gitea-mcp tool-handler signature that forgeHandler adapts.
type giteaToolHandler = func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error)

// wikiSlugSuffix is the marker Gitea appends to a wiki page's on-disk slug when
// the page name ends in a character it reserves (e.g. a trailing "."): the page
// "Foo." is stored and served as "Foo..-". gitea-mcp upstream does not handle
// this, so botfam used to carry a local gitea-mcp patch for it (agy, gitea-mcp
// 812d157). Pinning the submodule to the public upstream (#464) drops that
// patch, so we replicate the behavior here — in our own wrapper around the
// forge_wiki_* tools — rather than maintaining a private gitea-mcp fork.
const wikiSlugSuffix = ".-"

// wikiSlugFallback wraps a gitea-mcp wiki handler so escaped slugs are handled
// transparently:
//
//   - a page-targeted call (pageName set) that fails is retried once with the
//     ".-"-suffixed pageName, covering pages whose on-disk slug was escaped;
//   - ".-" is stripped from sub_url/html_url in successful results so callers
//     see the logical page URL.
//
// Non-page calls (e.g. "list") and already-suffixed names pass straight through.
func wikiSlugFallback(h giteaToolHandler) giteaToolHandler {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		res, err := h(ctx, req)
		if err == nil {
			return normalizeWikiResult(res), nil
		}
		pageName, _ := req.GetArguments()["pageName"].(string)
		if pageName == "" || strings.HasSuffix(pageName, wikiSlugSuffix) {
			return res, err
		}
		// Retry once with the escaped slug; a fresh args map keeps the caller's
		// request untouched.
		retry := req
		args := make(map[string]any, len(req.GetArguments()))
		for k, v := range req.GetArguments() {
			args[k] = v
		}
		args["pageName"] = pageName + wikiSlugSuffix
		retry.Params.Arguments = args
		if res2, err2 := h(ctx, retry); err2 == nil {
			return normalizeWikiResult(res2), nil
		}
		// Escaped retry didn't help; surface the original error.
		return res, err
	}
}

// normalizeWikiResult strips the ".-" escape suffix from sub_url/html_url fields
// in a wiki tool's JSON text result. It is best-effort: any content it cannot
// parse is returned unchanged.
func normalizeWikiResult(res *mcplib.CallToolResult) *mcplib.CallToolResult {
	if res == nil {
		return res
	}
	for i, c := range res.Content {
		tc, ok := mcplib.AsTextContent(c)
		if !ok {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(tc.Text), &v); err != nil {
			continue
		}
		if !stripWikiSuffixes(v) {
			continue
		}
		if b, err := json.Marshal(v); err == nil {
			res.Content[i] = mcplib.NewTextContent(string(b))
		}
	}
	return res
}

// stripWikiSuffixes walks a decoded JSON value and trims the ".-" suffix from
// any "sub_url"/"html_url" string fields, recursing into objects and arrays.
// Returns whether it changed anything.
func stripWikiSuffixes(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for _, key := range []string{"sub_url", "html_url"} {
			if s, ok := t[key].(string); ok && strings.HasSuffix(s, wikiSlugSuffix) {
				t[key] = strings.TrimSuffix(s, wikiSlugSuffix)
				changed = true
			}
		}
		for _, vv := range t {
			if stripWikiSuffixes(vv) {
				changed = true
			}
		}
	case []any:
		for _, vv := range t {
			if stripWikiSuffixes(vv) {
				changed = true
			}
		}
	}
	return changed
}
