# Hand-crafted correctness fixture with KNOWN violations.
# Expected:
#   violation_double     -> /i1
#   violation_ebd        -> /i2
#   violation_misattr    -> /i4
#   violation_incomplete -> /i3
#   violation_reconcile  -> /i4
#   violation_deadlock   -> /a, /b, /c
# Clean control: /i5 (one PR, closed, author == harness, no restart issue)

# /i1: two PRs -> double-exec
issue_created(/i1)@[2026-06-17T01:00:00].
issue_closed(/i1)@[2026-06-17T02:00:00].
pr_opened(/p1a, /i1)@[2026-06-17T01:10:00].
pr_opened(/p1b, /i1)@[2026-06-17T01:20:00].

# /i2: dispatched BEFORE created -> ebd
issue_created(/i2)@[2026-06-17T02:00:00].
dispatch(/t2, /i2, /h_claude)@[2026-06-17T01:30:00].

# /i3: frozen, never closed -> incomplete
frozen(/i3).
issue_created(/i3)@[2026-06-17T01:00:00].

# /i4: merge before restart, spawn after -> reconcile ; codex commit on claude dispatch -> misattr
issue_created(/i4)@[2026-06-17T03:00:00].
dispatch(/t4, /i4, /h_claude)@[2026-06-17T03:05:00].
pr_opened(/p4, /i4)@[2026-06-17T03:30:00].
pr_commit(/p4, /sha4).
commit_by(/sha4, /h_codex)@[2026-06-17T03:25:00].
pr_merged(/p4)@[2026-06-17T04:00:00].
restart(/e1)@[2026-06-17T05:00:00].
spawned(/t4b, /i4)@[2026-06-17T06:00:00].

# /i5: clean control (no violation)
issue_created(/i5)@[2026-06-17T00:10:00].
issue_closed(/i5)@[2026-06-17T00:50:00].
dispatch(/t5, /i5, /h_claude)@[2026-06-17T00:15:00].
pr_opened(/p5, /i5)@[2026-06-17T00:30:00].
pr_commit(/p5, /sha5).
commit_by(/sha5, /h_claude)@[2026-06-17T00:25:00].
pr_merged(/p5)@[2026-06-17T00:45:00].

# deadlock cycle a->b->c->a
blocked_on(/a, /b).
blocked_on(/b, /c).
blocked_on(/c, /a).
# non-cycle d->e (no deadlock)
blocked_on(/d, /e).
