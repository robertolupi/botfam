package forge

import (
	"fmt"
	"regexp"
)

// Scope selects a subset of forge issues. At most one of Milestone/Label/Epic
// should be set; All (or none) means the full history. Shared by the Mangle
// exporter, the forge linter, and the issue-graph builder so they agree on what
// an "epic closure" is.
type Scope struct {
	All       bool
	Milestone string // issues whose milestone title matches
	Label     string // issues carrying this label
	Epic      int    // issue number; its transitive task-list closure
}

// Issue-reference patterns: closing keywords (Gitea auto-close), bare mentions,
// and task-list checkboxes (`- [ ] #N` / `- [x] #N`) = an issue's children.
var (
	closesRe  = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)`)
	mentionRe = regexp.MustCompile(`#(\d+)`)
	taskRefRe = regexp.MustCompile(`(?m)^\s*[-*]\s*\[[ xX]\]\s*#(\d+)`)
)

func issueRefs(re *regexp.Regexp, s string) map[int]bool {
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

// TaskRefs returns the issue numbers in a body's task-list checkboxes — an
// issue's structural children (used for the --epic closure).
func TaskRefs(body string) map[int]bool { return issueRefs(taskRefRe, body) }

// ClosesRefs returns issue numbers referenced by Gitea closing keywords.
func ClosesRefs(s string) map[int]bool { return issueRefs(closesRe, s) }

// MentionRefs returns bare `#N` mentions.
func MentionRefs(s string) map[int]bool { return issueRefs(mentionRe, s) }

// SelectIssues returns the in-scope issue-number set, or nil for the full
// history (All / empty). Epic uses the transitive task-list closure (the same
// closure botfam sprint needs for CattleSprintScope).
func SelectIssues(issues []*Issue, sc Scope) map[int]bool {
	switch {
	case sc.Epic > 0:
		byNum := make(map[int]*Issue, len(issues))
		for _, iss := range issues {
			byNum[iss.Number] = iss
		}
		seen := map[int]bool{}
		queue := []int{sc.Epic}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if seen[n] {
				continue
			}
			seen[n] = true
			if iss := byNum[n]; iss != nil {
				for ref := range TaskRefs(iss.Body) {
					if !seen[ref] {
						queue = append(queue, ref)
					}
				}
			}
		}
		return seen
	case sc.Milestone != "":
		out := map[int]bool{}
		for _, iss := range issues {
			if iss.Milestone != nil && iss.Milestone.Title == sc.Milestone {
				out[iss.Number] = true
			}
		}
		return out
	case sc.Label != "":
		out := map[int]bool{}
		for _, iss := range issues {
			for _, l := range iss.Labels {
				if l.Name == sc.Label {
					out[iss.Number] = true
				}
			}
		}
		return out
	default:
		return nil
	}
}

// String is a human-readable description of the scope (for log lines / headers).
func (sc Scope) String() string {
	switch {
	case sc.Epic > 0:
		return fmt.Sprintf("epic #%d", sc.Epic)
	case sc.Milestone != "":
		return "milestone " + sc.Milestone
	case sc.Label != "":
		return "label " + sc.Label
	default:
		return "all"
	}
}
