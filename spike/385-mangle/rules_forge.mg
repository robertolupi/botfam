# Forge-native invariants — fire on REAL botfam history (no supervisor facts).
# Run: botfam mangle eval --from-store botfam.mg --file rules_forge.mg

Decl issue_created(Issue) temporal bound [/name].
Decl issue_closed(Issue) temporal bound [/name].
Decl issue_assignee(Issue, User) bound [/name, /name].
Decl pr_opened(PR, Issue) temporal bound [/name, /name].
Decl pr_merged(PR) temporal bound [/name].
Decl pr_commit(PR, Sha) bound [/name, /name].
Decl commit_by(Sha, Author) temporal bound [/name, /name].

# double-exec: an issue referenced by >1 distinct PR
# (project temporal -> non-temporal, then aggregate; avoids the O(N^2) self-join)
pr_iss_nt(P, I) :- pr_opened(P, I)@[T].
pr_per_issue(I, N) :- pr_iss_nt(P, I) |> do fn:group_by(I), let N = fn:count().
violation_double(I, N) :- pr_per_issue(I, N), :gt(N, 1).

# misattribution: a commit on a PR-for-issue authored by someone other than the
# issue's assignee (the real misattributed-work hazard)
violation_misattr(I, Author, Assignee) :-
  pr_opened(P, I)@[Tp], pr_commit(P, Sha), commit_by(Sha, Author)@[Tc],
  issue_assignee(I, Assignee), Author != Assignee.

# merged-but-open: a PR merged but its linked issue was never closed (hygiene)
closed_some(I) :- issue_closed(I)@[Tc].
violation_merged_open(I, P) :-
  pr_opened(P, I)@[Tp], pr_merged(P)@[Tm], !closed_some(I).
