# Forge-native invariants — fire on REAL botfam history (no supervisor facts).
# Schema matches `botfam mangle export` (#388): pr_closes vs pr_mentions split.
# Run: botfam mangle eval --from-store botfam.mg --file rules_forge.mg

Decl issue_created(Issue) temporal bound [/name].
Decl issue_closed(Issue) temporal bound [/name].
Decl issue_assignee(Issue, User) bound [/name, /name].
Decl pr_opened(PR) temporal bound [/name].
Decl pr_merged(PR) temporal bound [/name].
Decl pr_closes(PR, Issue) bound [/name, /name].
Decl pr_mentions(PR, Issue) bound [/name, /name].
Decl pr_commit(PR, Sha) bound [/name, /name].
Decl commit_by(Sha, Author) temporal bound [/name, /name].

# double-exec: an issue closed by >1 distinct PR (count distinct closers)
closed_by_count(I, N) :- pr_closes(P, I) |> do fn:group_by(I), let N = fn:count().
violation_double(I, N) :- closed_by_count(I, N), :gt(N, 1).

# misattribution: a commit on a PR-that-closes-I authored by someone other than
# I's assignee (the real misattributed-work hazard)
violation_misattr(I, Author, Assignee) :-
  pr_closes(P, I), pr_commit(P, Sha), commit_by(Sha, Author)@[Tc],
  issue_assignee(I, Assignee), Author != Assignee.

# merged-but-open: a PR that closes I was merged, but I was never closed (hygiene)
closed_some(I) :- issue_closed(I)@[Tc].
violation_merged_open(I, P) :-
  pr_closes(P, I), pr_merged(P)@[Tm], !closed_some(I).
