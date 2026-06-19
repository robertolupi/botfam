#!/bin/sh
# Bootstrap admin user, org, repository, and bot user tokens in the local test Forgejo.
set -eu

note() {
  printf 'bootstrap-test-forgejo: %s\n' "$*" >&2
}

issue_token() {
  local user=$1
  docker compose -f compose.test.yaml exec -T -u git forgejo \
    forgejo admin user generate-access-token --username "$user" --token-name "botfam-token-$user-$(date +%s)" --scopes all --raw
}

save_token() {
  local user=$1
  local token=$2

  mkdir -p "$HOME/.botfam"
  printf '%s' "$token" > "$HOME/.botfam/token-botfam-${user}-test"
}

# Wait for Forgejo to become healthy
note "Waiting for Forgejo container to become healthy..."
until docker compose -f compose.test.yaml exec -T forgejo sh -c 'nc -z 127.0.0.1 3000' >/dev/null 2>&1; do
  sleep 1
done

note "Forgejo is up and listening."

# Clean up any existing tokens/users if necessary by resetting user admin token, or just proceed on clean db
# (Since it's a test substrate, we usually run with clean volumes)

# Create admin user
note "Creating admin user..."
docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user create --username forgejo-admin --password adminpassword --email admin@example.com --admin --must-change-password=false || true

# Generate admin token
note "Generating admin access token..."
ADMIN_TOKEN=$(docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user generate-access-token --username forgejo-admin --token-name "admin-token-$(date +%s)" --scopes all --raw)

if [ -z "$ADMIN_TOKEN" ]; then
  note "Failed to generate admin token"
  exit 1
fi

# Create botfam organization
note "Creating organization 'botfam'..."
curl -s -X POST -H "Authorization: token $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"username": "botfam"}' \
  http://localhost:13000/api/v1/orgs >/dev/null || true

# Create botfam repository under organization
note "Creating repository 'botfam/botfam'..."
curl -s -X POST -H "Authorization: token $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d '{"name": "botfam"}' \
  http://localhost:13000/api/v1/orgs/botfam/repos >/dev/null || true

# Create bot users
note "Creating bot users..."
for user in agy-bot claude-bot codex-bot; do
  case "$user" in
    agy-bot) password=agybotpassword; email=agy-bot@example.com ;;
    claude-bot) password=claudebotpassword; email=claude-bot@example.com ;;
    codex-bot) password=codexbotpassword; email=codex-bot@example.com ;;
  esac
  docker compose -f compose.test.yaml exec -T -u git forgejo \
    forgejo admin user create --username "$user" --password "$password" --email "$email" --must-change-password=false >/dev/null 2>&1 || true
done

# Add bot users to the Owners team (ID 1)
note "Adding bot users to Owners team..."
for user in agy-bot claude-bot codex-bot; do
  curl -s -X PUT -H "Authorization: token $ADMIN_TOKEN" \
    "http://localhost:13000/api/v1/teams/1/members/$user" >/dev/null
done

note "Generating access tokens for bot users..."
AGY_TOKEN="$(issue_token agy-bot)"
CLAUDE_TOKEN="$(issue_token claude-bot)"
CODEX_TOKEN="$(issue_token codex-bot)"

# Save test tokens locally under ~/.botfam/
save_token "agy" "$AGY_TOKEN"
save_token "claude" "$CLAUDE_TOKEN"
save_token "codex" "$CODEX_TOKEN"
save_token "admin" "$ADMIN_TOKEN"

note "Bootstrap complete."
note "Saved tokens to ~/.botfam/token-botfam-admin-test, ~/.botfam/token-botfam-agy-test, ~/.botfam/token-botfam-claude-test, ~/.botfam/token-botfam-codex-test"
