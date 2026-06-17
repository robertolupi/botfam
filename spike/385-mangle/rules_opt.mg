# Optimized rule set: avoids the self-join cross-product for double-exec.
# Mangle has no join optimizer, so a self-join (pr_opened x pr_opened on I)
# materializes O(N^2). Fix = count-per-key via aggregation. But aggregation
# over a TEMPORAL predicate PANICS (engine bug), so first PROJECT the temporal
# predicate to a non-temporal one, then aggregate.

Decl issue_created(Issue) temporal bound [/name].
Decl issue_closed(Issue) temporal bound [/name].
Decl pr_opened(PR, Issue) temporal bound [/name, /name].
Decl pr_merged(PR) temporal bound [/name].
Decl commit_by(Sha, Author) temporal bound [/name, /name].
Decl dispatch(Task, Issue, Harness) temporal bound [/name, /name, /name].
Decl spawned(Task, Issue) temporal bound [/name, /name].
Decl restart(Epoch) temporal bound [/name].
Decl pr_commit(PR, Sha) bound [/name, /name].
Decl frozen(Issue) bound [/name].
Decl blocked_on(A, B) bound [/name, /name].

# double-exec via project-then-aggregate (linear, not O(N^2) self-join)
pr_opened_nt(P, I) :- pr_opened(P, I)@[T].
pr_count(I, N) :- pr_opened_nt(P, I) |> do fn:group_by(I), let N = fn:count().
violation_double(I) :- pr_count(I, N), :gt(N, 1).

violation_ebd(T, I) :-
  dispatch(T, I, _)@[Td], issue_created(I)@[Tc], :time:lt(Td, Tc).

violation_misattr(I, Sha, Author, H) :-
  dispatch(_, I, H)@[Td], pr_opened(P, I)@[Tp], pr_commit(P, Sha),
  commit_by(Sha, Author)@[Tc], Author != H.

closed_some(I) :- issue_closed(I)@[Tc].
violation_incomplete(I) :- frozen(I), !closed_some(I).

violation_reconcile(I) :-
  restart(_)@[Tr],
  pr_merged(P)@[Tm], pr_opened(P, I)@[To], :time:lt(Tm, Tr),
  spawned(_, I)@[Ts], :time:lt(Tr, Ts).

waits(A, B) :- blocked_on(A, B).
waits(A, C) :- blocked_on(A, B), waits(B, C).
violation_deadlock(A) :- waits(A, A).
