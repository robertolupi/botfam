#!/usr/bin/env bash
#
# test-forge-integration.sh — black-box e2e that proves the GO-NATIVE ccrep gate.
#
# Post-cleanup (#33/#34) there is no consensus code left to unit-test: the merge
# gate now lives entirely in Forgejo branch protection. This test stands up the
# hermetic Forgejo (compose.test.yaml, 127.0.0.1:13000), provisions the gate with
# tools/forge-gate.sh, lints it, and then drives the 0/1/2 approval ladder with
# three bot identities to prove the forge enforces quorum. If this passes, the
# gate survived the deletion — because it relocated to the forge.
#
# Requires Docker. Opt-in (slow); not part of `go test`. Hermetic: only ever
# talks to 127.0.0.1:13000, never the production gitea:3000.
#
# Usage: docker/test-forge-integration.sh [--keep]   (--keep: don't tear down)
#
set -uo pipefail
cd "$(dirname "$0")/.."

FORGE="http://localhost:13000"
COMPOSE="compose.test.yaml"
KEEP=""; [ "${1:-}" = "--keep" ] && KEEP=1
FAILS=0
note() { printf '\n=== %s ===\n' "$*"; }
ok()   { printf '  [PASS] %s\n' "$*"; }
bad()  { printf '  [FAIL] %s\n' "$*"; FAILS=$((FAILS+1)); }

teardown() {
  [ -n "$KEEP" ] && { note "leaving substrate up (--keep); 'docker compose -f $COMPOSE down -v' to clean"; return; }
  note "teardown"; docker compose -f "$COMPOSE" down -v >/dev/null 2>&1 || true
  rm -rf "${WORK:-/nonexistent}" 2>/dev/null || true
}
trap teardown EXIT

# admin CLI helper (matches bootstrap-test-forgejo.sh)
fadmin() { docker compose -f "$COMPOSE" exec -T -u git forgejo forgejo "$@"; }
# API helpers: areq <token> <method> <path> [json] ; returns body. acode: returns HTTP status.
areq()  { local t="$1" m="$2" p="$3" b="${4:-}"; if [ -n "$b" ]; then curl -s -X "$m" -H "Authorization: token $t" -H "Content-Type: application/json" -d "$b" "$FORGE/api/v1$p"; else curl -s -X "$m" -H "Authorization: token $t" "$FORGE/api/v1$p"; fi; }
acode() { local t="$1" m="$2" p="$3" b="${4:-}"; if [ -n "$b" ]; then curl -s -o /dev/null -w '%{http_code}' -X "$m" -H "Authorization: token $t" -H "Content-Type: application/json" -d "$b" "$FORGE/api/v1$p"; else curl -s -o /dev/null -w '%{http_code}' -X "$m" -H "Authorization: token $t" "$FORGE/api/v1$p"; fi; }
jget()  { python3 -c "import sys,json; d=json.load(sys.stdin); print(d$1)"; }

# ---------------------------------------------------------------------------
note "1. bring up Forgejo"
docker compose -f "$COMPOSE" up -d forgejo >/dev/null 2>&1
until docker compose -f "$COMPOSE" exec -T forgejo sh -c 'nc -z 127.0.0.1 3000' >/dev/null 2>&1; do sleep 1; done
ok "forgejo healthy on $FORGE"

note "2. bootstrap (org/repo/bots/tokens) + a 3rd reviewer"
sh docker/bootstrap-test-forgejo.sh
# 3rd user so required=2 is testable (author + two distinct approvers)
fadmin admin user create --username carol-bot --password carolbotpass --email carol-bot@example.com --must-change-password=false >/dev/null 2>&1 || true
curl -s -X PUT -H "Authorization: token $(fadmin admin user generate-access-token --username forgejo-admin --token-name boot-$(date +%s%N) --scopes all --raw)" "$FORGE/api/v1/teams/1/members/carol-bot" >/dev/null 2>&1 || true
ADMIN_TOKEN="$(fadmin admin user generate-access-token --username forgejo-admin --token-name e2e-$(date +%s%N) --scopes all --raw)"
CAROL_TOKEN="$(fadmin admin user generate-access-token --username carol-bot --token-name e2e-$(date +%s%N) --scopes all --raw)"
AGY_TOKEN="$(tr -d '\r\n' < "$HOME/.botfam/token-botfam-agy-test")"
CLAUDE_TOKEN="$(tr -d '\r\n' < "$HOME/.botfam/token-botfam-claude-test")"
[ -n "$ADMIN_TOKEN" ] && [ -n "$CAROL_TOKEN" ] && [ -n "$AGY_TOKEN" ] && [ -n "$CLAUDE_TOKEN" ] && ok "users + tokens (claude/agy/carol/admin)" || bad "token setup"

note "3. initialize repo via 'botfam credential' (exercises the push helper, #4/#212)"
WORK="$(mktemp -d)"; REPO_DIR="$WORK/botfam"
git clone -q "$FORGE/botfam/botfam.git" "$REPO_DIR" 2>/dev/null || { git init -q "$REPO_DIR"; git -C "$REPO_DIR" remote add origin "$FORGE/botfam/botfam.git"; }
# Build the binary so the helper is the real `botfam credential` subcommand.
BOTFAM_BIN="$WORK/botfam-bin"
go build -o "$BOTFAM_BIN" ./cmd/botfam >/dev/null 2>&1 || { bad "go build botfam (for credential helper)"; BOTFAM_BIN=""; }
push_as() { # push_as <user> <token-literal> <args...>
  local user="$1" tok="$2"; shift 2
  local tf; tf="$(mktemp)"; printf '%s' "$tok" > "$tf"
  BOTFAM_FORGE_HOST="localhost:13000" BOTFAM_FORGE_USER="$user" BOTFAM_TOKEN_FILE="$tf" \
    git -C "$REPO_DIR" -c "credential.$FORGE.helper=!$BOTFAM_BIN credential" "$@"
  local rc=$?; rm -f "$tf"; return $rc
}
( cd "$REPO_DIR"
  git config user.email claude-bot@example.com; git config user.name claude-bot
  echo "# botfam test" > README.md; git add README.md; git commit -q -m "init" )
if [ -n "$BOTFAM_BIN" ]; then
  push_as claude-bot "$CLAUDE_TOKEN" push -q origin HEAD:refs/heads/botfam-next 2>/dev/null \
    && ok "git push via 'botfam credential' (created botfam-next)" || bad "helper push failed"
fi
sleep 2
areq "$ADMIN_TOKEN" PATCH /repos/botfam/botfam '{"default_branch":"botfam-next"}' >/dev/null

note "4. provision + lint the go-native gate (forge-gate.sh apply/check)"
tools/forge-gate.sh apply --host "$FORGE" --repo botfam/botfam --branch botfam-next --token "$ADMIN_TOKEN" --approvals 2 >/dev/null \
  && ok "forge-gate apply" || bad "forge-gate apply"
if chk_out=$(tools/forge-gate.sh check --host "$FORGE" --repo botfam/botfam --branch botfam-next --token "$ADMIN_TOKEN" --approvals 2 2>&1); then
  ok "forge-gate check (gate config correct)"; else bad "forge-gate check on the test forge"; echo "$chk_out" | sed 's/^/      /'; fi

# open a PR authored by claude-bot
open_pr() { # open_pr <branch> <file> -> echoes PR number
  local br="$1" f="$2"
  ( cd "$REPO_DIR"
    git fetch -q origin botfam-next
    git checkout -q -B "$br" FETCH_HEAD
    echo "change $(date +%s%N)" > "$f"; git add "$f"; git commit -q -m "$br" )
  push_as claude-bot "$CLAUDE_TOKEN" push -q -f origin "$br" 2>/dev/null
  areq "$CLAUDE_TOKEN" POST /repos/botfam/botfam/pulls "{\"head\":\"$br\",\"base\":\"botfam-next\",\"title\":\"$br\"}" | jget "['number']"
}
review() { areq "$1" POST "/repos/botfam/botfam/pulls/$2/reviews" "{\"event\":\"$3\"}" >/dev/null; }
merge_code() { acode "$1" POST "/repos/botfam/botfam/pulls/$2/merge" '{"Do":"merge"}'; }

note "5. approval ladder (0 -> 1 -> 2) on PR authored by claude"
PR="$(open_pr feat-1 a.txt)"; [ -n "$PR" ] && ok "claude opened PR #$PR" || bad "PR open"
c=$(merge_code "$ADMIN_TOKEN" "$PR"); [ "$c" -ge 400 ] && ok "0 approvals: merge blocked (HTTP $c)" || bad "0 approvals: merge NOT blocked (HTTP $c)"
review "$CLAUDE_TOKEN" "$PR" APPROVED 2>/dev/null  # author self-approve (should not count)
c=$(merge_code "$ADMIN_TOKEN" "$PR"); [ "$c" -ge 400 ] && ok "author self-approve doesn't count: still blocked (HTTP $c)" || bad "author self-approve counted (HTTP $c)"
review "$AGY_TOKEN" "$PR" APPROVED; c=$(merge_code "$ADMIN_TOKEN" "$PR")
[ "$c" -ge 400 ] && ok "1 independent approval: still blocked (HTTP $c)" || bad "1 approval: merge NOT blocked (HTTP $c)"
review "$CAROL_TOKEN" "$PR" APPROVED; c=$(merge_code "$ADMIN_TOKEN" "$PR")
[ "$c" = 200 ] && ok "2 independent approvals: merge SUCCEEDS (HTTP $c)" || bad "2 approvals: merge failed (HTTP $c)"

note "6. negative: request_changes blocks even with enough approvals"
PR2="$(open_pr feat-2 b.txt)"
review "$AGY_TOKEN" "$PR2" APPROVED; review "$CAROL_TOKEN" "$PR2" REQUEST_CHANGES
c=$(merge_code "$ADMIN_TOKEN" "$PR2"); [ "$c" -ge 400 ] && ok "request_changes blocks merge (HTTP $c)" || bad "request_changes did NOT block (HTTP $c)"

note "7. dismiss-stale: a new commit drops prior approvals"
PR3="$(open_pr feat-3 c.txt)"
review "$AGY_TOKEN" "$PR3" APPROVED; review "$CAROL_TOKEN" "$PR3" APPROVED
( cd "$REPO_DIR"; git checkout -q feat-3; echo "more" >> c.txt; git add c.txt; git commit -q -m "amend feat-3" )
push_as claude-bot "$CLAUDE_TOKEN" push -q origin feat-3 2>/dev/null
# Forgejo processes the PR sync + approval dismissal asynchronously; poll.
active=2
for _ in $(seq 1 20); do
  active=$(areq "$ADMIN_TOKEN" GET "/repos/botfam/botfam/pulls/$PR3/reviews" | python3 -c "import sys,json; print(sum(1 for r in json.load(sys.stdin) if r.get('state')=='APPROVED' and not r.get('stale')))" 2>/dev/null || echo 2)
  [ "$active" = 0 ] && break
  sleep 1
done
[ "$active" = 0 ] && ok "new commit dismissed all approvals (active=$active)" || bad "approvals not dismissed after poll (active=$active)"
c=$(merge_code "$ADMIN_TOKEN" "$PR3"); [ "$c" -ge 400 ] && ok "post-dismiss: merge blocked again (HTTP $c)" || bad "post-dismiss: merge NOT blocked (HTTP $c)"

note "RESULT"
if [ "$FAILS" = 0 ]; then echo "ALL PASS — go-native gate enforces quorum end-to-end"; exit 0; else echo "$FAILS check(s) FAILED"; exit 1; fi
