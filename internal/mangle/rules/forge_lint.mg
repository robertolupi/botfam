# Curated forge-linter rule set (botfam#389) — v1. Runs over a `botfam mangle
# export` snapshot to surface process hazards the wiki names. Schema = the
# exporter's. Each violation_* head is reported by `botfam mangle lint`.
#
# Authoring rules (mangle-go quirks, see spike/385-mangle/SPIKE_RESULTS.md):
#   - bind temporal time with @[T], never @[_] (interval-intersection gotcha);
#   - project temporal -> non-temporal before negation/aggregation.

Decl issue_created(Issue) temporal bound [/name].
Decl issue_closed(Issue) temporal bound [/name].
Decl issue_assignee(Issue, User) bound [/name, /name].
Decl pr_opened(PR) temporal bound [/name].
Decl pr_merged(PR) temporal bound [/name].
Decl pr_closes(PR, Issue) bound [/name, /name].
Decl pr_mentions(PR, Issue) bound [/name, /name].
Decl pr_commit(PR, Sha) bound [/name, /name].
Decl commit_by(Sha, Author) temporal bound [/name, /name].

closed_some(I) :- issue_closed(I)@[Tc].

# misattributed-work: a commit on a PR-that-closes-I authored by someone other
# than I's assignee (wiki antipattern-misattributed-work)
violation_misattributed(I, Author, Assignee) :-
  pr_closes(P, I), pr_commit(P, Sha), commit_by(Sha, Author)@[Tc],
  issue_assignee(I, Assignee), Author != Assignee.

# double-close: one issue closed by >1 distinct PR
closed_by_count(I, N) :- pr_closes(P, I) |> do fn:group_by(I), let N = fn:count().
violation_double_close(I, N) :- closed_by_count(I, N), :gt(N, 1).

# merged-but-open: a PR that closes I was merged, but I is still open (hygiene)
violation_merged_open(I, P) :-
  pr_closes(P, I), pr_merged(P)@[Tm], !closed_some(I).

# DEFERRED (needs the exporter to emit a snapshot-now fact + :time arithmetic):
#   hard-issue starvation (open + unassigned + aged > 30d). The modal age form
#   `<-[30d, _] issue_created(I)` does not match instant facts in mangle-go, so
#   age must be computed against an emitted `now` via fn:time:sub / :time:lt.
