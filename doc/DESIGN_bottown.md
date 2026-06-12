# botfam → bottown — sketch (future)

> [!NOTE]
> **Status**: Tabled/Pivoted (2026-06-11). The REST-based `bottown` design has
> been superseded by the IRC-first coordination system. Networking, identity
> authentication (via NickServ), presence tracking, and total-ordered message
> sequencing are now natively handled by the Ergo IRC server in Docker rather
> than a custom HTTP/REST daemon.

Status: **Sketch / future.** Not a spec yet — a place to park decisions so they
survive. bottown is botfam's networked sibling: the same model for agents that
do **not** share a filesystem.

botfam serves one machine; identity and the fam namespace are derived from the
local git repo. bottown serves agents over the network — possibly nothing in
common locally — so the things botfam *derives*, bottown must *declare*, and
the things botfam makes *optional*, bottown makes *mandatory*.

______________________________________________________________________

## Decisions captured so far

- **Transport: REST** (plain HTTP + JSON), not a bespoke protocol. Trivial to
  implement in Go (`net/http`), trivial to test (`curl`), callable from
  anything.
- **Blocking `recv` → long-poll.** `GET .../inbox?wait=120` holds the
  connection until a message arrives; `204 No Content` on timeout. Simpler and
  more universal than SSE for "block until one message" — and it keeps the
  zero-token-wait property that justifies blocking in the first place.
- **Namespace: an explicit `topic` in config** (the "street name"). There is no
  shared local repo to derive a fam key from, so the group is *declared*. This
  is the network analog of botfam's auto-derived
  `~/.botfam/<slug>-<rootcommit>/`.
- **Identity: bearer token → actor**, mapped server-side; the actor is never
  client-asserted, so it can't be spoofed. This is botfam's opt-in *lock*
  (DESIGN.md §3) promoted to the default — the lock was bottown previewed.

## The key structural idea: MCP stays the agent API

REST is bottown's **server** protocol; agents keep speaking **MCP**. A thin
local stdio shim (botfam in a "town client" mode) translates the *same*
`send/recv/try_recv/inbox/post/claim/…` tool calls into REST requests against
the town server. So the **agent-facing tool surface is identical** to botfam —
only the backend swaps:

```
botfam :  MCP tools ──> maildir on local fs
bottown:  MCP tools ──> [local shim] ──REST──> town server ──> store
```

All of Phase 1's tool design carries over unchanged; bottown is a transport,
not a redesign. (This mirrors scriba's gitea-mcp bridge — but as a ~200-line
REST service plus tokens, not a Gitea stack.)

## Rough REST surface (illustrative, not final)

```
Authorization: Bearer <token>          # token → (actor, topic scope)

POST /t/{topic}/messages                # send       {to,type,payload,in_reply_to?}
GET  /t/{topic}/inbox?wait=120          # recv (long-poll; 204 on timeout)
GET  /t/{topic}/inbox                   # try_recv / snapshot
POST /t/{topic}/tasks                   # post
POST /t/{topic}/tasks/claim             # claim (server picks one; atomic)
POST /t/{topic}/tasks/{id}/complete     # complete   (also heartbeat/abandon)
POST /t/{topic}/tasks/sweep             # coordinator: reclaim expired leases
```

## Open questions (defer)

- **Server store:** maildir-on-the-server (keep the audit trail) vs.
  SQLite/embedded.
- **Token scope:** per-topic vs. per-actor-across-topics; issuance/rotation.
- **TLS / deployment:** localhost-only first, then LAN with certs.
- **CCREP over REST:** Phase 2's ledger exposed the same way, or town-only.
- **Lost+found / dead-letter.** botfam uses lazy mailbox creation, so a
  misaddressed message just sits in a phantom mailbox. With server-side
  validation against a roster, bottown can route unknown recipients and expired
  (`expires_at`) mail to a `lost+found` topic instead of silently dropping
  them.

bottown is **after** botfam proves the model. Build botfam first; let real use
tell us which of the above actually matter.
