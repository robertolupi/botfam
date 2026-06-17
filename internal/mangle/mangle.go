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
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/robertolupi/botfam/internal/forge"
	"github.com/robertolupi/botfam/internal/mangle/interp"
)

// ExportOptions controls which slice of forge history to materialize.
type ExportOptions struct {
	WithCommits bool // pull per-PR commits (author identity) — the slow part
}

// ExportStats reports what was materialized and how long acquisition took.
type ExportStats struct {
	Issues, Pulls, Commits int
	Duration               time.Duration
}

var issueRef = regexp.MustCompile(`#(\d+)`)

// Export materializes the full forge history as Mangle facts to w.
func Export(c *forge.Client, opt ExportOptions, w io.Writer) (ExportStats, error) {
	start := time.Now()
	var st ExportStats

	fmt.Fprintf(w, "# botfam forge history -> Mangle facts (%s/%s)\n", c.Owner, c.Repo)
	fmt.Fprintf(w, "# generated %s\n\n", time.Now().UTC().Format(time.RFC3339))

	issues, err := c.ListAllIssues()
	if err != nil {
		return st, fmt.Errorf("list issues: %w", err)
	}
	for _, iss := range issues {
		st.Issues++
		id := issueID(iss.Number)
		if t := mTime(iss.CreatedAt); t != "" {
			fmt.Fprintf(w, "issue_created(%s)@[%s].\n", id, t)
		}
		if t := mTime(iss.ClosedAt); t != "" {
			fmt.Fprintf(w, "issue_closed(%s)@[%s].\n", id, t)
		}
		for _, a := range iss.Assignees {
			fmt.Fprintf(w, "issue_assignee(%s, %s).\n", id, nameC(a.Login))
		}
	}

	pulls, err := c.ListAllPulls()
	if err != nil {
		return st, fmt.Errorf("list pulls: %w", err)
	}
	for _, pr := range pulls {
		st.Pulls++
		pid := prID(pr.Number)
		// link PR -> issue(s) referenced in title/body (closes/fixes #N etc.)
		for _, m := range issueRef.FindAllStringSubmatch(pr.Title+" "+pr.Body, -1) {
			iid := "/i" + m[1]
			if t := mTime(pr.CreatedAt); t != "" {
				fmt.Fprintf(w, "pr_opened(%s, %s)@[%s].\n", pid, iid, t)
			}
		}
		if pr.Merged {
			if t := mTime(pr.MergedAt); t != "" {
				fmt.Fprintf(w, "pr_merged(%s)@[%s].\n", pid, t)
			}
		}
		if opt.WithCommits {
			commits, err := c.GetPullCommits(pr.Number)
			if err != nil {
				return st, fmt.Errorf("pull %d commits: %w", pr.Number, err)
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

// ---- Mangle name/time formatting --------------------------------------------

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
