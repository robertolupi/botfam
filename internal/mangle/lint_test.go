package mangle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robertolupi/botfam/internal/mangle/interp"
)

// TestForgeLintRules exercises the embedded curated rule set against a small
// fixture with one planted instance of each v1 violation.
func TestForgeLintRules(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.mg")
	if err := os.WriteFile(rules, []byte(forgeLintRules), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "facts.mg")
	facts := `
# /i1: assigned alice, closed by PR p1 whose commit is by bob -> misattributed
issue_created(/i1)@[2026-01-01T00:00:00].
issue_closed(/i1)@[2026-01-02T00:00:00].
issue_assignee(/i1, /alice).
pr_closes(/p1, /i1).
pr_commit(/p1, /sha1).
commit_by(/sha1, /bob)@[2026-01-01T12:00:00].

# /i2: closed by two distinct PRs -> double_close
pr_closes(/p2, /i2).
pr_closes(/p3, /i2).

# /i3: a merged PR closes it, but i3 never closed -> merged_open
pr_closes(/p4, /i3).
pr_merged(/p4)@[2026-01-03T00:00:00].
issue_created(/i3)@[2026-01-01T00:00:00].

# clean control /i9: assigned alice, closed by p9 authored by alice, issue closed
issue_created(/i9)@[2026-01-01T00:00:00].
issue_closed(/i9)@[2026-01-02T00:00:00].
issue_assignee(/i9, /alice).
pr_closes(/p9, /i9).
pr_commit(/p9, /sha9).
commit_by(/sha9, /alice)@[2026-01-01T12:00:00].
`
	if err := os.WriteFile(fixture, []byte(facts), 0o644); err != nil {
		t.Fatal(err)
	}

	results, _, err := interp.Run(rules, fixture, "violation", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range results {
		got[r.Predicate] = len(r.Rows)
	}
	want := map[string]int{
		"violation_misattributed": 1,
		"violation_double_close":  1,
		"violation_merged_open":   1,
	}
	for pred, n := range want {
		if got[pred] != n {
			t.Errorf("%s = %d, want %d (all: %v)", pred, got[pred], n, got)
		}
	}
}
