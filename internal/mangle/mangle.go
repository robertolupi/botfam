// Package mangle exports botfam forge history as Mangle (temporal Datalog)
// facts and evaluates rule files against them. It backs `botfam mangle`.
//
// Design: materialize-then-evaluate. The exporter pulls a forge snapshot once
// (measure acquisition separately) and writes a .mg fact file; eval loads
// rules+facts into the in-memory engine and queries. Facts are NOT resolved
// lazily during evaluation — the engine re-scans relations during the
// fixpoint, so lazy forge calls would multiply RPC and couple eval latency to
// forge availability. See wiki CattleInvariantsAsLogic.
package mangle

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mangle/interp"
)

//go:embed rules/forge_lint.mg
var forgeLintRules string

// ExportOptions selects which slice of forge history to materialize (forge.Scope)
// and whether to pull per-PR commits.
type ExportOptions struct {
	forge.Scope
	WithCommits bool // pull per-PR commits (author identity) — the slow part
}

// ExportStats reports what was materialized and how long acquisition took.
type ExportStats struct {
	Issues, Pulls, Commits int
	Duration               time.Duration
}

// Export materializes forge history (optionally a subset) as Mangle facts.
func Export(ctx context.Context, c *forge.Client, opt ExportOptions, w io.Writer) (ExportStats, error) {
	start := time.Now()
	var st ExportStats

	issues, err := c.ListAllIssues(ctx)
	if err != nil {
		return st, fmt.Errorf("list issues: %w", err)
	}
	target := forge.SelectIssues(issues, opt.Scope) // nil => everything

	fmt.Fprintf(w, "# botfam forge history -> Mangle facts (%s/%s)\n", c.Owner, c.Repo)
	fmt.Fprintf(w, "# generated %s; scope=%s\n\n", time.Now().UTC().Format(time.RFC3339), opt.Scope.String())

	for _, iss := range issues {
		if target != nil && !target[int(iss.Index)] {
			continue
		}
		st.Issues++
		id := issueID(int(iss.Index))
		if t := mTime(iss.Created.UTC().Format(time.RFC3339)); t != "" {
			fmt.Fprintf(w, "issue_created(%s)@[%s].\n", id, t)
		}
		if iss.Closed != nil {
			if t := mTime(iss.Closed.UTC().Format(time.RFC3339)); t != "" {
				fmt.Fprintf(w, "issue_closed(%s)@[%s].\n", id, t)
			}
		}
		for _, a := range iss.Assignees {
			fmt.Fprintf(w, "issue_assignee(%s, %s).\n", id, nameC(a.UserName))
		}
	}

	pulls, err := c.ListAllPulls(ctx)
	if err != nil {
		return st, fmt.Errorf("list pulls: %w", err)
	}
	for _, pr := range pulls {
		closes := forge.ClosesRefs(pr.Title + "\n" + pr.Body)
		mentions := forge.MentionRefs(pr.Title + "\n" + pr.Body)
		if target != nil && !intersects(target, closes) && !intersects(target, mentions) {
			continue
		}
		st.Pulls++
		pid := prID(int(pr.Index))
		if pr.Created != nil {
			if t := mTime(pr.Created.UTC().Format(time.RFC3339)); t != "" {
				fmt.Fprintf(w, "pr_opened(%s)@[%s].\n", pid, t)
			}
		}
		if pr.HasMerged && pr.Merged != nil {
			if t := mTime(pr.Merged.UTC().Format(time.RFC3339)); t != "" {
				fmt.Fprintf(w, "pr_merged(%s)@[%s].\n", pid, t)
			}
		}
		for n := range closes {
			fmt.Fprintf(w, "pr_closes(%s, %s).\n", pid, issueID(n))
		}
		for n := range mentions {
			if !closes[n] { // a closing ref is not also a bare mention
				fmt.Fprintf(w, "pr_mentions(%s, %s).\n", pid, issueID(n))
			}
		}
		if opt.WithCommits {
			commits, err := c.GetPullCommits(ctx, int(pr.Index))
			if err != nil {
				return st, fmt.Errorf("pull %d commits: %w", pr.Index, err)
			}
			for _, cm := range commits {
				st.Commits++
				sha := nameC("c" + cm.SHA)
				fmt.Fprintf(w, "pr_commit(%s, %s).\n", pid, sha)
				if t := mTime(cm.Commit.Author.Date); t != "" {
					fmt.Fprintf(w, "commit_by(%s, %s)@[%s].\n", sha, nameC(cm.AuthorLogin()), t)
				}
			}
		}
	}

	st.Duration = time.Since(start)
	return st, nil
}

// Eval loads ruleFile + storeFile into the engine and queries every head
// predicate matching prefix (default "violation"). Returns per-predicate rows
// and the engine wall-clock.
func Eval(ruleFile, storeFile, prefix string, out io.Writer) ([]interp.Result, time.Duration, error) {
	return interp.Run(ruleFile, storeFile, prefix, out)
}

// LintStats reports the forge-linter run timing.
type LintStats struct {
	Export   ExportStats
	EvalTime time.Duration
}

// Lint materializes a forge snapshot for the selected scope and evaluates the
// embedded curated rule set (rules/forge_lint.mg) over it — the forge-linter
// (botfam#389, use case C). Returns per-rule violations.
func Lint(ctx context.Context, c *forge.Client, opt ExportOptions, progress io.Writer) ([]interp.Result, LintStats, error) {
	var ls LintStats

	facts, err := os.CreateTemp("", "botfam-lint-facts-*.mg")
	if err != nil {
		return nil, ls, err
	}
	defer os.Remove(facts.Name())
	st, err := Export(ctx, c, opt, facts)
	facts.Close()
	if err != nil {
		return nil, ls, err
	}
	ls.Export = st

	rules, err := os.CreateTemp("", "botfam-lint-rules-*.mg")
	if err != nil {
		return nil, ls, err
	}
	defer os.Remove(rules.Name())
	if _, err := io.WriteString(rules, forgeLintRules); err != nil {
		return nil, ls, err
	}
	rules.Close()

	results, dur, err := interp.Run(rules.Name(), facts.Name(), "violation", progress)
	ls.EvalTime = dur
	return results, ls, err
}

// ---- helpers ----------------------------------------------------------------

func intersects(set map[int]bool, sub map[int]bool) bool {
	for n := range sub {
		if set[n] {
			return true
		}
	}
	return false
}

func issueID(n int) string { return fmt.Sprintf("/i%d", n) }
func prID(n int) string    { return fmt.Sprintf("/pr%d", n) }

var nonName = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

// nameC turns an arbitrary string into a Mangle name constant (/foo_bar).
func nameC(s string) string {
	s = nonName.ReplaceAllString(strings.TrimSpace(s), "_")
	if s == "" {
		s = "unknown"
	}
	return "/" + s
}

// mTime parses an RFC3339 timestamp and renders it as a Mangle datetime
// literal in UTC (no offset). Empty/zero inputs yield "".
func mTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "0001-01-01") {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05")
}
