# Cattle invariants as temporal Datalog — spike for botfam#385
# Canonical query shapes from CattleInvariantsAsLogic.

# ---- temporal event schema (interval-bearing) ----
Decl issue_created(Issue) temporal bound [/name].
Decl issue_closed(Issue) temporal bound [/name].
Decl pr_opened(PR, Issue) temporal bound [/name, /name].
Decl pr_merged(PR) temporal bound [/name].
Decl commit_by(Sha, Author) temporal bound [/name, /name].
Decl dispatch(Task, Issue, Harness) temporal bound [/name, /name, /name].
Decl spawned(Task, Issue) temporal bound [/name, /name].
Decl restart(Epoch) temporal bound [/name].

# ---- non-temporal schema ----
Decl pr_commit(PR, Sha) bound [/name, /name].
Decl frozen(Issue) bound [/name].
Decl blocked_on(A, B) bound [/name, /name].

# NOTE (Mangle temporal gotcha): a temporal atom read with @[_] imposes an
# INTERVAL-INTERSECTION constraint; two instant-facts at different times never
# overlap, so such a join silently yields nothing. To get a relational join
# "regardless of time", bind the instant to a variable @[T] (even if unused).

# A1. double-exec: two distinct PRs for one issue  (join + inequality)
violation_double(I, P1, P2) :-
  pr_opened(P1, I)@[T1], pr_opened(P2, I)@[T2], P1 != P2.

# A2. externalize-before-depend: dispatched before the issue was created (temporal order)
violation_ebd(T, I) :-
  dispatch(T, I, _)@[Td], issue_created(I)@[Tc], :time:lt(Td, Tc).

# A3. misattribution: commit author != dispatched harness  (cross-relation join)
violation_misattr(I, Sha, Author, H) :-
  dispatch(_, I, H)@[Td],
  pr_opened(P, I)@[Tp],
  pr_commit(P, Sha),
  commit_by(Sha, Author)@[Tc],
  Author != H.

# A4. liveness (finished log): frozen issue never closed  (stratified negation)
closed_some(I) :- issue_closed(I)@[Tc].
violation_incomplete(I) :- frozen(I), !closed_some(I).

# A5. reconciliation across restart: merge-before-restart but spawn-after  (happens-before across a marker)
violation_reconcile(I) :-
  restart(_)@[Tr],
  pr_merged(P)@[Tm], pr_opened(P, I)@[To], :time:lt(Tm, Tr),
  spawned(_, I)@[Ts], :time:lt(Tr, Ts).

# A6. deadlock: transitive closure + cycle  (recursion)
waits(A, B) :- blocked_on(A, B).
waits(A, C) :- blocked_on(A, B), waits(B, C).
violation_deadlock(A) :- waits(A, A).
