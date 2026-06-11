# Proposal: Runtime Coverage Profiling for Dormant-Code Detection

> [!NOTE]
> **Status**: Draft (2026-06-11). Operator-initiated ("I think we should do
> it"); needs fam evaluation before code lands.

## Status

**Draft** (2026-06-11). Proposed by Roberto during the first full operator
code review; drafted by claude. Pending CCREP evaluation.

| Field | Value |
|---|---|
| Proposal id | TBD |
| Executor | claude |
| Quorum | majority |
| Deadline | none |

## Problem

This codebase was bootstrapped entirely by agents: the same actors write the
code, write the tests, and review both. In that loop, **test coverage cannot
detect dormant code** — tests are precisely the mechanism that keeps dead
paths green forever. Coverage answers "does a test execute this?", never "does
operation execute this?".

Concrete evidence from the 2026-06-11 review:

| Package | Unit coverage | Operational status |
|---|---|---|
| `internal/store` | 69.9% | live (via CLI) |
| `internal/mcp` | 30.5% | **dormant** — no harness configures `botfam serve` since the IRC pivot |
| `internal/fam` | 20.8% | live — every workflow runs through it |
| `internal/server` | 2.1% | dormant-ish — UDS daemon, legacy substrate |

The dormant MCP package out-covers the most-used package in the repo. Plain
coverage is also misleading in the other direction: the 15 integration tests
in `cmd/botfam` exec a separately compiled binary, so their (substantial)
real coverage counts toward no package.

This matters now because the IRC-first pivot (2026-06-11) deliberately kept
the legacy substrate — MCP stdio server, UDS daemon, SQLite mailbox/queue —
as a fallback, and PROTOCOL.md already names its retirement as a pending
proposal. Retiring it should be an evidence-based decision ("nothing executed
this path in N sessions"), not a vibes-based one. The same applies to the
MCP-era entries in `doc/KNOWN_ISSUES.md`, which retire with the code they
describe.

## Proposed Behavior

Use Go's native binary coverage (Go ≥ 1.20): build the installed `botfam`
binary with `-cover`, point each agent's environment at a coverage directory,
and periodically diff **operational coverage** against **test coverage**.
Anything green in tests but black in operation is a dormancy candidate.

1. **Instrumented builds.** `bootstrap-botfam.sh` gains a `--coverage` mode
   (or it becomes the default for fam installs) that adds `-cover` to the
   `go build` invocation at the existing build site (the `-ldflags` line).
2. **Per-agent collection.** `botfam setup` creates
   `~/.botfam/covdata/<actor>/` and the generated harness env sets
   `GOCOVERDIR` to it, so every real CLI invocation an agent makes deposits
   counters there. No agent behavior changes; collection is passive.
3. **Reporting.** A `botfam coverage` subcommand (or initially a script)
   wraps `go tool covdata`:
   - `botfam coverage percent` — merged operational coverage per package.
   - `botfam coverage dormant` — the diff: functions covered by
     `go test ./... -coverprofile` but with zero operational counts, grouped
     by package. This is the actionable output.
4. **Cadence.** Read the dormancy report at session retrospectives (the
   existing `botfam-session-retrospective` skill is the natural hook) and as
   required evidence attached to any CCREP proposal that deletes code.

### Rollout

- **Phase 0 (zero code, can start today):** build by hand with
  `go build -cover`, export `GOCOVERDIR` in the worktree env, run normal
  sessions for a day, inspect with `go tool covdata percent`. Validates the
  data is worth the plumbing.
- **Phase 1:** the bootstrap/setup/reporting changes above, via normal
  `!propose` with a sha.

## Costs and Risks

- **Overhead:** counter increments only; negligible for a short-lived CLI.
- **Data loss on kill:** counters flush on normal exit; a SIGKILLed process
  (e.g. the Gatekeeper issue in KNOWN_ISSUES §3) writes nothing. Acceptable —
  dormancy detection needs aggregate signal, not completeness.
- **Accumulation:** covdata directories grow; `coverage report` should sweep
  or rotate (e.g. per-session subdirs, prune on report).
- **Interpretation hazard:** operational coverage measures what *did* run,
  not what *should*. Newly shipped features start at zero and must not be
  auto-flagged; the dormant report needs an age/grace convention (e.g. only
  flag code older than N sessions). It complements review; it does not
  replace it.
- **Scope:** instrumented binaries are for fam-internal use only; release
  builds (if ever) stay uninstrumented.

## First Expected Payoff

The legacy-substrate retirement decision: after a week of instrumented
operation, `botfam coverage dormant` should show `internal/mcp` and most of
`internal/server` at zero operational counts, turning "delete the fallback"
from an argument into a one-line evidence citation — and closing
KNOWN_ISSUES §3, §7, and §19 with it.
