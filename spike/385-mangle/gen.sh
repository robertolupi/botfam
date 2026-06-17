#!/usr/bin/env bash
# Generate N issues of synthetic facts (~7 facts/issue, mostly clean),
# injecting each violation type at ~1% and a single deadlock cycle.
# Times: clean issues use a fixed ordered set so no false ebd/reconcile.
N=${1:-400}
T0=2026-06-01T00:00:00   # created
T1=2026-06-01T00:01:00   # dispatch (after created -> no ebd)
T2=2026-06-01T00:02:00   # pr opened
T3=2026-06-01T00:03:00   # merged/closed
awk -v N="$N" -v T0="$T0" -v T1="$T1" -v T2="$T2" -v T3="$T3" 'BEGIN{
  for(i=1;i<=N;i++){
    printf "issue_created(/i%d)@[%s].\n", i, T0
    printf "issue_closed(/i%d)@[%s].\n", i, T3
    printf "dispatch(/t%d, /i%d, /h_claude)@[%s].\n", i, i, T1
    printf "pr_opened(/p%d, /i%d)@[%s].\n", i, i, T2
    printf "pr_commit(/p%d, /s%d).\n", i, i
    printf "commit_by(/s%d, /h_claude)@[%s].\n", i, T2
    printf "pr_merged(/p%d)@[%s].\n", i, T3
    # inject violations every 100th issue
    if(i%100==0){
      printf "pr_opened(/pX%d, /i%d)@[%s].\n", i, i, T2          # double-exec
      printf "commit_by(/s%d, /h_codex)@[%s].\n", i, T2          # misattr (2nd author on same sha)
      printf "frozen(/iF%d).\n", i                                # incomplete (frozen, never closed)
      printf "issue_created(/iF%d)@[%s].\n", i, T0
      printf "dispatch(/tE%d, /iE%d, /h_claude)@[%s].\n", i, i, T1  # ebd: dispatch T1 ...
      printf "issue_created(/iE%d)@[%s].\n", i, T3                  # ... created T3 (after) -> ebd
    }
  }
  # restarts/epochs are RARE in reality: fixed small count, not 1% of N.
  # (restart shares no join key with the rule body -> cross-product; keep it tiny)
  for(j=1;j<=5;j++){
    printf "pr_merged(/pR%d)@[%s].\n", j, T1
    printf "pr_opened(/pR%d, /iR%d)@[%s].\n", j, j, T0
    printf "restart(/e%d)@[%s].\n", j, T2
    printf "spawned(/tR%d, /iR%d)@[%s].\n", j, j, T3   # merge<restart<spawn -> reconcile
  }
  # wait-for graph is SMALL in reality (concurrently-blocked tasks), so fix it
  # at K nodes regardless of history size. One 3-cycle closes a deadlock.
  K=30
  for(i=1;i<K;i++) printf "blocked_on(/n%d, /n%d).\n", i, i+1
  printf "blocked_on(/n3, /n1).\n"   # closes a 3-cycle n1->n2->n3->n1
}'
