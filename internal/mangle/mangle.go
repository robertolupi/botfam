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

// ExportOptions selects which slice of forge history to materialize. At most
// one selector should be set; none means the full history (--all).
type ExportOptions struct {
	WithCommits bool   // pull per-PR commits (author identity) — the slow part
	Milestone   string // issues whose milestone title matches
	Label       string // issues carrying this label
	Epic        int    // issue number; export its transitive #N closure
}

// ExportStats reports what was materialized and how long acquisition took.
type ExportStats struct {
	Issues, Pulls, Commits int
	Duration               time.Duration
}

// closesRe matches Gitea's auto-close keywords; mentionRe matches bare #N.
var (
	closesRe  = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)
	mentionRe = regexp.MustCompile(`#(\d+)`)
	// taskRefRe matches an epic's structural children: task-list checkboxes
	// `- [ ] #N` / `- [x] #N`. Used for --epic closure (not prose mentions).
	taskRefRe = regexp.MustCompile(`(?m)^\s*[-*]\s*\[[ xX]\]\s*#(\d+)`)
)

// Export materializes forge history (optionally a subset) as Mangle facts.
func Export(c *forge.Client, opt ExportOptions, w io.Writer) (ExportStats, error) {
	start := time.Now()
	var st ExportStats

	issues, err := c.ListAllIssues()
	if err != nil {
		return st, fmt.Errorf("list issues: %w", err)
	}
	target := selectIssues(issues, opt) // nil => everything

	fmt.Fprintf(w, "# botfam forge history -> Mangle facts (%s/%s)\n", c.Owner, c.Repo)
	fmt.Fprintf(w, "# generated %s; scope=%s\n\n", time.Now().UTC().Format(time.RFC3339), scopeLabel(opt))

	for _, iss := range issues {
		if target != nil && !target[iss.Number] {
			continue
		}
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
		closes := refs(closesRe, pr.Title+"\n"+pr.Body)
		mentions := refs(mentionRe, pr.Title+"\n"+pr.Body)
		if target != nil && !intersects(target, closes) && !intersects(target, mentions) {
			continue
		}
		st.Pulls++
		pid := prID(pr.Number)
		if t := mTime(pr.CreatedAt); t != "" {
			fmt.Fprintf(w, "pr_opened(%s)@[%s].\n", pid, t)
		}
		if pr.Merged {
			if t := mTime(pr.MergedAt); t != "" {
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

// selectIssues returns the target issue-number set for the selector, or nil
// for the full history. Epic uses a transitive #N closure over issue bodies
// (the same closure botfam sprint needs for CattleSprintScope).
func selectIssues(issues []*forge.Issue, opt ExportOptions) map[int]bool {
	switch {
	case opt.Epic > 0:
		byNum := make(map[int]*forge.Issue, len(issues))
		for _, iss := range issues {
			byNum[iss.Number] = iss
		}
		seen := map[int]bool{}
		queue := []int{opt.Epic}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if seen[n] {
				continue
			}
			seen[n] = true
			if iss := byNum[n]; iss != nil {
				for ref := range refs(taskRefRe, iss.Body) {
					if !seen[ref] {
						queue = append(queue, ref)
					}
				}
			}
		}
		return seen
	case opt.Milestone != "":
		out := map[int]bool{}
		for _, iss := range issues {
			if iss.Milestone != nil && iss.Milestone.Title == opt.Milestone {
				out[iss.Number] = true
			}
		}
		return out
	case opt.Label != "":
		out := map[int]bool{}
		for _, iss := range issues {
			for _, l := range iss.Labels {
				if l.Name == opt.Label {
					out[iss.Number] = true
				}
			}
		}
		return out
	default:
		return nil
	}
}

func scopeLabel(opt ExportOptions) string {
	switch {
	case opt.Epic > 0:
		return fmt.Sprintf("epic #%d", opt.Epic)
	case opt.Milestone != "":
		return "milestone " + opt.Milestone
	case opt.Label != "":
		return "label " + opt.Label
	default:
		return "all"
	}
}

// Eval loads ruleFile + storeFile into the engine and queries every head
// predicate matching prefix (default "violation"). Returns per-predicate rows
// and the engine wall-clock.
func Eval(ruleFile, storeFile, prefix string, out io.Writer) ([]interp.Result, time.Duration, error) {
	return interp.Run(ruleFile, storeFile, prefix, out)
}

// ---- helpers ----------------------------------------------------------------

func refs(re *regexp.Regexp, s string) map[int]bool {
	out := map[int]bool{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		if n > 0 {
			out[n] = true
		}
	}
	return out
}

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
