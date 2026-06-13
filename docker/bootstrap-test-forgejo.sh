#!/bin/sh
# /Users/rlupi/src/fams/botfam/wt-agy/docker/bootstrap-test-forgejo.sh
# Bootstrap admin user, org, repository, and bot user tokens in the local test Forgejo.
set -eu

note() {
  printf 'bootstrap-test-forgejo: %s\n' "$*" >&2
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

# Create agy-bot and claude-bot users
note "Creating bot users..."
docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user create --username agy-bot --password agybotpassword --email agy-bot@example.com --must-change-password=false || true

docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user create --username claude-bot --password claudebotpassword --email claude-bot@example.com --must-change-password=false || true

# Add bot users to the Owners team (ID 1)
note "Adding bot users to Owners team..."
curl -s -X PUT -H "Authorization: token $ADMIN_TOKEN" \
  http://localhost:13000/api/v1/teams/1/members/agy-bot >/dev/null
curl -s -X PUT -H "Authorization: token $ADMIN_TOKEN" \
  http://localhost:13000/api/v1/teams/1/members/claude-bot >/dev/null

# Generate tokens for bot users
note "Generating access tokens for bot users..."
AGY_TOKEN=$(docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user generate-access-token --username agy-bot --token-name "botfam-token-agy-$(date +%s)" --scopes all --raw)

CLAUDE_TOKEN=$(docker compose -f compose.test.yaml exec -T -u git forgejo \
  forgejo admin user generate-access-token --username claude-bot --token-name "botfam-token-claude-$(date +%s)" --scopes all --raw)

# Save test tokens locally under ~/.botfam/
mkdir -p "$HOME/.botfam"
echo "$AGY_TOKEN" > "$HOME/.botfam/token-botfam-agy-test"
echo "$CLAUDE_TOKEN" > "$HOME/.botfam/token-botfam-claude-test"

note "Bootstrap complete."
note "Saved tokens to ~/.botfam/token-botfam-agy-test and ~/.botfam/token-botfam-claude-test"
