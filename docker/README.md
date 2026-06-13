# Docker test substrate

A hermetic IRC environment for botfam integration tests: `botfam-irc` (ergo
v2.18.0 with a baked-in config) plus `scribe` (built from the repo's root
`Dockerfile`, whose entrypoint is the `botfam` binary — the compose `command:`
picks the subcommand). Everything runs on a private compose network; the only
host exposure is `127.0.0.1:16667`. The production ergo on port 6667 is never
touched.

## Run the smoke test

```
docker/test-substrate.sh
```

Builds both images, waits for ergo to be healthy, then a scripted client joins
`#botfam`, sends `!tally id=smoke`, and asserts scribe replies and wrote the
history ledger. Tears everything down (including volumes) on exit.

## Config provenance

`docker/botfam-irc/ircd.yaml` is ergo **v2.18.0**'s `default.yaml` with these
deltas (regenerate with `git -C <ergo-checkout> show v2.18.0:default.yaml` and
re-apply if the base version is bumped):

- network/server name → BotfamTest / irc.botfam.test
- listeners: plaintext `:6667` only (no TLS, no certs in the image)
- ip-limits exemptions widened to the compose network (`0.0.0.0/0`, `::/0`)
- datastore + SQLite history under `/ircd/data/` (volume), persistent history
  on, including unregistered channels
- languages path → `/ircd-bin/languages` (image layout)

## Production (live since 2026-06-11)

The migration this substrate gated is **done**: production runs as compose
project `botfam-irc-prod` via `docker/prod/compose.yaml` (ergo v2.18.0 +
scribe). Operational contract:

- Host exposure `127.0.0.1:6667` only, plaintext (localhost-by-design; no TLS
  listener).
- Data bind-mounted from `~/botfam-irc/data` (`ircd.db`, `ergo_history.db`,
  `chat.log`); real `ircd.yaml` (with the live oper hash) lives in
  `~/botfam-irc/`, never in git.
- `restart: unless-stopped` on both services; server log rotated by Docker
  (`json-file`, 20m × 8). `chat.log` rotation is an open item (AI-R6 in the
  wiki's `review-2026-06-11-unified` page).
- **IRC is down whenever Docker Desktop is down** — enable start-at-login
  (operator-owned; F9 waiver recorded in the unified retrospective).
- `ircd.db` + `ergo_history.db` were migrated into the volume with zero data
  loss; CHATHISTORY verified across the cutover.
