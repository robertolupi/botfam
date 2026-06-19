package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func wikiReq(args map[string]any) mcplib.CallToolRequest {
	var r mcplib.CallToolRequest
	r.Params.Arguments = args
	return r
}

func textOf(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("nil/empty result")
	}
	tc, ok := mcplib.AsTextContent(res.Content[0])
	if !ok {
		t.Fatalf("first content is not text")
	}
	return tc.Text
}

func TestWikiSlugFallback_RetriesEscapedSlug(t *testing.T) {
	var calls []string
	h := func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		pn, _ := req.GetArguments()["pageName"].(string)
		calls = append(calls, pn)
		if pn == "Foo." { // raw name fails; only the escaped on-disk slug exists
			return nil, errors.New("404 page not found")
		}
		return mcplib.NewToolResultText(`{"title":"Foo.","sub_url":"/wiki/Foo..-"}`), nil
	}

	res, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{
		"method": "get", "pageName": "Foo.",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "Foo." {
		t.Fatalf("calls = %v, want [Foo. Foo.%s]", calls, wikiSlugSuffix)
	}
	if calls[1] != "Foo."+wikiSlugSuffix {
		t.Fatalf("retry pageName = %q, want %q", calls[1], "Foo."+wikiSlugSuffix)
	}
	// Result URL must be normalized (".-" stripped).
	if got := textOf(t, res); strings.Contains(got, wikiSlugSuffix+`"`) || !strings.Contains(got, `"/wiki/Foo."`) {
		t.Fatalf("sub_url not normalized: %s", got)
	}
}

func TestWikiSlugFallback_UpdateRetryPreservesLogicalTitle(t *testing.T) {
	// On an update retry for an escaped slug, the title must stay the logical
	// name — not the ".-" storage slug — or the page gets renamed. (#476 review)
	var retryArgs map[string]any
	h := func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		pn, _ := req.GetArguments()["pageName"].(string)
		if pn == "Foo." {
			return nil, errors.New("404")
		}
		retryArgs = req.GetArguments()
		return mcplib.NewToolResultText(`{}`), nil
	}
	if _, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{
		"method": "update", "pageName": "Foo.", "content": "x", // no title
	})); err != nil {
		t.Fatal(err)
	}
	if retryArgs["pageName"] != "Foo."+wikiSlugSuffix {
		t.Fatalf("retry pageName = %v", retryArgs["pageName"])
	}
	if retryArgs["title"] != "Foo." {
		t.Fatalf("retry title = %v, want logical %q (not the escaped slug)", retryArgs["title"], "Foo.")
	}
}

func TestWikiSlugFallback_UpdateRetryKeepsExplicitTitle(t *testing.T) {
	var retryArgs map[string]any
	h := func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if pn, _ := req.GetArguments()["pageName"].(string); pn == "Foo." {
			return nil, errors.New("404")
		}
		retryArgs = req.GetArguments()
		return mcplib.NewToolResultText(`{}`), nil
	}
	if _, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{
		"method": "update", "pageName": "Foo.", "content": "x", "title": "Renamed",
	})); err != nil {
		t.Fatal(err)
	}
	if retryArgs["title"] != "Renamed" {
		t.Fatalf("retry title = %v, want caller's explicit %q", retryArgs["title"], "Renamed")
	}
}

func TestWikiSlugFallback_NoRetryWhenSuccess(t *testing.T) {
	calls := 0
	h := func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		calls++
		return mcplib.NewToolResultText(`{"sub_url":"/wiki/Home"}`), nil
	}
	if _, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{"method": "get", "pageName": "Home"})); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on success)", calls)
	}
}

func TestWikiSlugFallback_OriginalErrorWhenRetryAlsoFails(t *testing.T) {
	h := func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return nil, errors.New("boom")
	}
	_, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{"method": "get", "pageName": "Missing"}))
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want original 'boom'", err)
	}
}

func TestWikiSlugFallback_NoPageNamePassesThrough(t *testing.T) {
	calls := 0
	h := func(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		calls++
		return nil, errors.New("list failed")
	}
	// "list" has no pageName: must not retry.
	if _, err := wikiSlugFallback(h)(context.Background(), wikiReq(map[string]any{"method": "list"})); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry without pageName)", calls)
	}
}

func TestStripWikiSuffixes_Array(t *testing.T) {
	v := []any{
		map[string]any{"sub_url": "/wiki/A.-", "html_url": "http://x/wiki/A.-"},
		map[string]any{"sub_url": "/wiki/B"},
	}
	if !stripWikiSuffixes(v) {
		t.Fatal("expected change")
	}
	first := v[0].(map[string]any)
	if first["sub_url"] != "/wiki/A" || first["html_url"] != "http://x/wiki/A" {
		t.Fatalf("not stripped: %v", first)
	}
}
