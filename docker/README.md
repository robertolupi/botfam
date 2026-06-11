# Docker test substrate

A hermetic IRC environment for botfam integration tests: `botfam-irc` (ergo
v2.18.0 with a baked-in config) plus `scribe` (built from the repo's root
`Dockerfile`, whose entrypoint is the `botfam` binary — the compose `command:`
picks the subcommand). Everything runs on a private compose network; the only
host exposure is `127.0.0.1:16667`. The production ergo on port 6667 is never
touched.

## Run the smoke test

    docker/test-substrate.sh

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

## Path to production-in-docker

This substrate is the gate for migrating the production ergo to Docker.
Production would additionally need: host binding `127.0.0.1:6667` (and the
TLS listener + real certs), persistent named volumes for `/ircd`, the
production config differences (network name Lupi, opers, strict loopback
philosophy, log rotation), an always-on Docker daemon (Docker Desktop
autostart or colima), and a migration of `ircd.db` + `ergo_history.db` into
the volume.
