---
name: code-editing
description: Use when editing Go code — especially for refactors, symbol renames, cross-package type changes, or exploring an unfamiliar package's API. Replaces manual grep+Read workflows with gopls MCP and codebase-memory-mcp tools that are faster, complete, and catch errors earlier. Trigger on "rename", "refactor", "replace type", "find all uses of", "callers of", "what fields does X have", or any time you reach for grep on Go source.
---

# Code-editing tool protocol

Before reaching for `grep`, `Read`, or `go build` on Go source, check the table
below. The MCP tools are faster, return complete results, and surface errors
while you are still in the relevant file — not after 20 edits.

This skill was filed as [#412](http://gitea:3000/botfam/botfam/issues/412) after
a 20-file type-alias migration that took 30+ Edit calls because the agent
defaulted to manual grep instead of the tools below.

## Available tools

### gopls MCP (`mcp__gopls__*`)

| Tool | Use it when |
|---|---|
| `go_symbol_references` | Finding every use of a function, type, or field across the repo |
| `go_rename_symbol` | Renaming a symbol or field across all callers atomically |
| `go_diagnostics` | Checking for compile errors and vet findings after a batch of edits |
| `go_package_api` | Inspecting a package's exported API — types, fields, method signatures |
| `go_file_context` | Getting type/hover info for a specific symbol at a file position |
| `go_search` | Fuzzy symbol search by name pattern |

**Not exposed by the gopls MCP** (editor-only): Hover, Inlay Hints, Semantic
Tokens, Folding Range, Document Link, Selection Range, Call Hierarchy, Assembly,
Split Package. Do not attempt to call these.

### codebase-memory-mcp

| Tool | Use it when |
|---|---|
| `search_graph(name_pattern=...)` | Locating all symbols matching a pattern across packages |
| `trace_path(direction="inbound")` | Finding all callers of a function or consumers of a type |
| `detect_changes()` | Mapping the current git diff to affected symbols before starting |
| `get_code_snippet(qualified_name=...)` | Reading the exact source of a known symbol |

## Decision rules — use the tool, not the fallback

| You are about to… | Use instead |
|---|---|
| `grep -rn "iss\.Number"` across Go files | `go_symbol_references` on `Number` field |
| `sed` / Edit to rename a field in N files | `go_rename_symbol` — one call, all callers |
| `go build ./...` to check for errors | `go_diagnostics` on the affected package(s) |
| `Read` a vendor or module file to find field names | `go_package_api` on the import path |
| Guess a field name from context | `go_file_context` at the call site to see the type |
| Read 10 files to find what calls a function | `trace_path(direction="inbound")` |

## Session protocol for refactors

Follow these steps at the start of any session that involves renaming,
type-swapping, or cross-package changes:

1. **Scope** — call `detect_changes()` to see which symbols the current diff
   already touches. Avoids re-doing work already in progress.

2. **Blast radius** — for each type or function being changed, call
   `trace_path(direction="inbound")` to enumerate every consumer. Build the
   full list *before* editing, so no callers are missed.

3. **API** — for any external package whose types you are adopting, call
   `go_package_api` on its import path. Read the exact field names and types
   before writing a single Edit. Do not guess.

4. **Edit** — make changes. After each file or logical group:
   - Run `go_diagnostics` on the affected package. Fix errors in that file before
     moving on. Do not accumulate errors across 20 edits and discover them at the end.

5. **Rename atomically** — if a symbol or field is being renamed across callers,
   use `go_rename_symbol` rather than `go_symbol_references` + N Edit calls.
   `go_rename_symbol` is atomic and handles unexported fields, embedded types, and
   interface satisfaction.

6. **Final check** — `go build ./...` and `go test ./...` as a gate before
   committing. This is a sanity check, not the primary error-detection loop.

## Worked example — what went wrong and what the tool path looks like

**Scenario:** migrate `forge.Issue.Number int` → `sdk.Issue.Index int64` across
8 packages.

**What the agent did (slow path):**
```
grep -rn "iss\.Number" ./...          # 12 results across 6 files
grep -rn "issue\.Number" ./...        # 8 more results
# ... 4 more grep variants
# Read scope.go, graph.go, mangle.go, session_extract.go, wait.go, ...
# 20+ Edit calls
# go build → 30 errors (missed pr.Head.SHA vs pr.Head.Sha, missed test files)
# Fix errors → go build again
```

**Tool path (fast path):**
```
# 1. Scope
detect_changes()
→ shows forge/client.go, forge/history.go touched

# 2. Blast radius
trace_path(function_name="sdkIssueToLocal", direction="inbound")
→ returns: scope.go, history.go, client.go, issues.go (complete, 4 files, not 8 guessed)

# 3. API
go_package_api(import_path="gitea.dev/sdk", symbol="Issue")
→ returns: Index int64 `json:"number"`, State StateType, Poster *User,
           Created time.Time, Closed *time.Time, Labels []*Label, ...
           (exact field names, no guessing SHA vs Sha)

# 4. Edit + diagnose per file
go_diagnostics(package="github.com/robertolupi/botfam/internal/forge")
→ immediate errors after each file, not a batch at the end

# 5. Rename (if applicable)
go_rename_symbol(file="internal/forge/scope.go", line=57, new_name="Index")
→ renames across all callers atomically
```

The tool path eliminates the grep loop, the guessing, and the batch-error
discovery. In the actual migration it would have reduced ~30 Edit calls to ~10.
