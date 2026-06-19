# Online crashpoint (use case B): child_exit recently, no ledger_append since.
# Negated modal (![-[...]) does NOT parse, so lift the positive modal to a
# derived predicate and negate THAT (stratified negation). now = wallclock.
# Replace the timestamps with values within the window of the eval time.

Decl child_exit(Task) temporal bound [/name].
Decl ledger_append(Task) temporal bound [/name].

# /tA: exited recently, no ledger_append -> crashpoint
# /tB: exited recently AND ledger appended -> NOT a crashpoint
# /tC: exited long ago (outside window) -> NOT a crashpoint
# (fill instants near `now` when running, e.g. via date -u -v-2M)

exit_recent(T)   :- <-[0s, 300s] child_exit(T).
ledger_recent(T) :- <-[0s, 300s] ledger_append(T).
crashpoint(T)    :- exit_recent(T), !ledger_recent(T).
