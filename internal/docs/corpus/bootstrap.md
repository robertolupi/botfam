# Same-Harness Coordination (BOOTSTRAP primitives)

> **The host-local / same-harness coordination layer.** Cross-harness
> coordination uses the live substrate — IRC for pings/wakes, the forge + git
> compare-and-swap for durable shared state (see `botfam:///docs/protocol`).
> **This** document is the zero-dependency layer for organizing work *within a
> single filesystem/host*: one OS primitive (atomic `rename`) gives you atomic
> publish, compare-and-swap, and a leased work queue — no lock daemon, no etcd,
> no host-local lock file.
>
> **Capability, not trust.** These primitives give the coordination *mechanism*
> only. They do **not** give attributable writes, real identity, or
> confabulation-resistance — an actor here is just the name a writer uses. That
> trust layer is what the compiled runtime adds (see §5). Use the bare
> primitives for cooperative same-host work; rely on the runtime when
> participants are independent, numerous, or unsupervised.

## 1. The one primitive: atomic `rename`

`rename(2)` within a single filesystem is atomic: a file is either fully at its
old path or fully at its new one — never half-moved, never half-read. Two
patterns follow.

**Atomic publish** — never let a reader see a half-written file:

```
write   tmp/<id>                 # build the whole file out of sight
rename  tmp/<id> -> <dest>/<id>  # appears complete, instantly
```

**Compare-and-swap (CAS)** — atomically claim a contested item. Create the
destination directory *first*, so a failed rename means exactly one thing:

```
mkdir -p claimed/<me>/
rename  open/<f> -> claimed/<me>/<f>
#   success           -> you own it
#   ENOENT on source  -> someone else won the race; try the next item
```

That is the whole engine. Everything below is convention on top of it.

## 2. Where state lives

Host-local coordination state lives **outside any git repo** — in a scratch/tmp
area or a shared fam root (`$COLLAB_ROOT`, default `~/.botfam/{{.Fam}}/`). It is
ephemeral and rebuildable, never the source of truth, so keep it out of version
control. Name files so a plain lexical sort is chronological — e.g. a
zero-padded-nanos prefix `<nanos>-<rand>` — so "oldest first" needs no parsing.

## 3. Identity is a convention

An actor's name is the basename of its worktree (`wt-<actor>` -> `<actor>`).
This is a **label, not an identity**: nothing here stops a writer from using
another name. The compiled runtime binds the name to the process that actually
wrote; the bare primitives trust the label. Fine for cooperative fams — see §5.

## 4. A leased work queue (claim / lease / sweep)

The CAS primitive gives a work queue with failover and no background process:

- **post** — atomic-publish a task into `tasks/open/`.
- **claim** — `mkdir -p tasks/claimed/<me>/`, then CAS the oldest
  `tasks/open/<f> -> tasks/claimed/<me>/<f>`. Success = yours; ENOENT = someone
  else took it, try the next. Then record `owner`, `claimed_at`, and
  `lease_expires_at = now + ttl` (generous, e.g. 600s).
- **heartbeat** — extend `lease_expires_at` at every natural pause, or risk
  being swept.
- **complete** — move the file to `tasks/done/`.
- **sweep** — *any* actor, opportunistically before each claim: return any claim
  whose `lease_expires_at` is past to `tasks/open/` (record who/when swept).

The sweep is the failover: a claimant that stalls or dies (compaction, token
exhaustion, an operator interrupt) does not deadlock the queue — its lease
lapses and the item returns. This is why host-local coordination needs no lock
manager: a lock has a holder that can die; a lease + sweep recovers
automatically, and a CAS claim has no holder at all.

## 5. What the compiled runtime adds (trust)

The bare primitives are capability without trust. The runtime keeps the same
semantics and adds the layer markdown cannot:

- **Attributable, tamper-evident writes** — a sole-writer-per-actor lock makes
  "who wrote this" real. (This is the legitimate use of a host-local lock:
  write attribution, *not* inter-actor coordination.)
- **Real identity binding** — the actor is bound to the process that wrote, so
  you cannot act *as* someone else.
- **Confabulation-resistance** — machine-derived proposals/approvals fill SHAs
  from the VCS and refuse retyped or stale ones.
- **Race-closed primitives** — ownership-proving updates and precise lease
  expiry.

Pick by your stakes: a small, fully-cooperative, human-supervised fam can run on
the bare primitives; a large, fast, or unsupervised one needs the runtime,
because capability without trust is how confident fabrications end up in a log
nobody can trust.
