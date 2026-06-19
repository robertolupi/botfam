# Spike results — temporal Mangle for Cattle invariants (botfam#385)

Engine: `codeberg.org/TauCeti/mangle-go` @ v0.5.0-19-g4dcaa58 (submodule `third_party/mangle-go`).
Built clean with `go build ./interpreter/mg` (go 1.26). All runs use the `mg` interpreter
(`--load rules,facts --exec <query>`). Hardware: M-series laptop.

## TL;DR

**The temporal story is real — the make-or-break question passes.** All six canonical query
shapes are expressible and correct, including the temporal *reconciliation-across-restart*
property and the modal *crashpoint*. At realistic botfam scale (~current history) the whole
program evaluates in **0.07 s**. But the engine has **no join optimizer/indexing**, so several
rule shapes are super-linear; at 10×/100× projection they blow up without rule-level care.
Four real rough edges surfaced (it is a young single-maintainer fork, as warned).

**Verdict: adopt for A (offline assertions) and C (prod hazards) at realistic scale, as a
careful-authoring tool. Keep the fact schema engine-neutral (engine swappable). Do not put it
on a hot path (B online crashpoints) at scale without bounded windows.**

## Correctness (use case A) — PASS

Hand-crafted fixture with planted violations + a clean control issue. Every rule returns
exactly the planted violations and nothing on the clean control:

| shape | rule feature | result |
|---|---|---|
| double-exec | join + inequality | ✓ /i1 |
| externalize-before-depend | temporal order `:time:lt` | ✓ /i2 |
| misattribution | 4-way cross-relation join | ✓ /i4 |
| incomplete (liveness) | stratified negation | ✓ /i3 |
| reconciliation-across-restart | happens-before across a `restart` marker | ✓ /i4 |
| deadlock | recursion / transitive closure | ✓ /a,/b,/c |

## Temporal + crashpoint (use case B) — works, with a workaround

- Modal operators parse and evaluate: `<-[0s,300s] child_exit(T)` etc. `now` = wallclock at eval.
- **Negated modal does not parse** (`![-[...]`). Workaround: lift the positive modal to a derived
  predicate and negate *that* (stratified): `crashpoint(T) :- exit_recent(T), !ledger_recent(T).`
  Returns exactly the expected task. ✓

## Performance — the catch

Full program, realistic fixture cardinality (rare restarts, small wait-graph):

| N issues | facts | full-program eval | RSS |
|---|---|---|---|
| 400 (≈ current botfam) | 2.9k | **0.07 s** | 33 MB |
| 4 000 (10×) | 28k | 4.5 s | 201 MB |
| 40 000 (100×) | 282k | did not finish < ~3 min (as-written) | — |

Per-rule, the cost is **nested-loop joins with no indexing**:

| rule | shape | N=400 → 4000 | class |
|---|---|---|---|
| incomplete, deadlock(small graph) | negation / small recursion | ~0.16 s flat | fine |
| ebd | 2-way temporal join | 0.70 s | ~linear-ish |
| misattr | 4-way join | 0.05 → 1.22 s | super-linear (~N^1.4) |
| double-exec (self-join) | pr × pr on issue | O(N²) — blows up | quadratic |
| reconcile (global `restart`) | unkeyed marker × merges | O(restarts × merges) | quadratic if restarts ∝ N |
| deadlock on a *chain* | transitive closure | O(N²) | quadratic |

### Mitigations found
- **Self-join → aggregation** (`group_by` + `fn:count`, flag count > 1): linear, handles 40 000 in **1.5 s**.
  BUT **aggregation over a temporal predicate PANICS** (engine bug) → first *project* the temporal
  predicate to a non-temporal one, then aggregate. See `rules_opt.mg`.
- Keep marker relations (`restart`) and wait-graphs (`blocked_on`) small — they are, in reality.
- Multi-way relational joins (misattr) have no easy fix; remain super-linear. Acceptable only at
  realistic scale.

## Real rough edges found (the fork's least-proven parts)

1. **Docs don't match the grammar.** The readthedocs temporal examples (`Decl x(person) temporal.`)
   fail to parse against the shipped ANTLR grammar. Use the tests as ground truth, not the docs.
2. **`@[_]` interval-intersection gotcha.** Reading a temporal atom with a wildcard time imposes an
   interval-overlap constraint; instants at different times never overlap → **silent empty joins**.
   Always bind the instant to a variable `@[T]` for a relational join.
3. **Negated modal unparseable** (`![-[...]`); lift to a derived predicate (above).
4. **temporal + aggregation panics** — hard engine crash at `engine/transformer.go:133`. Worth filing
   upstream. Workaround: project temporal → non-temporal before aggregating.

## Verdict vs #385 acceptance criteria

- [x] Queries return correct results — **yes**.
- [x] Well under a second at current scale — **yes** (0.07 s at N≈400).
- [ ] No non-termination at 10×/100× — **no as-written**; needs rule-level care, and some shapes
      (multi-way joins) stay super-linear regardless.
- [x] Verdict recorded — **adopt for A and C at realistic scale; keep the schema engine-neutral so
      the engine is swappable; do not hot-path B at scale.** The temporal capability is genuine;
      the engine is young (4 bugs in an afternoon). File the temporal+aggregation panic upstream;
      check whether official Google Mangle is gaining DatalogMTL to de-risk the fork dependency.

## Files
- `rules.mg` — canonical rules (with the `@[T]` binding note).
- `rules_opt.mg` — optimized double-exec (project-then-aggregate).
- `fixture.mg` — correctness fixture with planted violations.
- `gen.sh` — synthetic fact generator (`bash gen.sh N`).
- `crashpoint.mg` — modal crashpoint via derived-predicate negation.
