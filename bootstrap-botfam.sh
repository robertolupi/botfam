#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  bootstrap-botfam.sh REPO --agents agy,codex,claude [options]

Options:
  --agents LIST          Comma-separated agent names. Required.
  --project NAME         botfam project name. Defaults to REPO basename.
  --worktree-dir DIR     Parent directory for wt-<agent> worktrees.
                         Defaults to REPO parent.
  --no-worktrees         Do not create per-agent git worktrees.
  --botfam-bin PATH      Use this botfam binary.
  --install-bin PATH     Build botfam here if no binary is found.
                         Defaults to $HOME/bin/botfam.
  --force                Pass --force to botfam setup and allow overwriting
                         existing JSON harness configs when jq is unavailable.
  -h, --help             Show this help.
EOF
}

die() {
  printf 'bootstrap-botfam: %s\n' "$*" >&2
  exit 1
}

note() {
  printf 'bootstrap-botfam: %s\n' "$*" >&2
}

json_string() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/^/"/; s/$/"/'
}

abs_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s\n' "$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")" ;;
  esac
}

split_agents() {
  printf '%s' "$1" | tr ',' ' '
}

write_json_mcp_config() {
  path=$1
  bin=$2
  mkdir -p "$(dirname "$path")"
  tmp="${path}.tmp.$$"
  if [ -f "$path" ]; then
    if command -v jq >/dev/null 2>&1; then
      jq --arg command "$bin" '
        .mcpServers = (.mcpServers // {}) |
        .mcpServers.collab = {"command": $command}
      ' "$path" > "$tmp"
    elif [ "$force" -eq 1 ]; then
      command_json=$(json_string "$bin")
      cat > "$tmp" <<EOF
{
  "mcpServers": {
    "collab": {
      "command": $command_json
    }
  }
}
EOF
    else
      die "$path exists and jq is unavailable; install jq or rerun with --force to overwrite it"
    fi
  else
    command_json=$(json_string "$bin")
    cat > "$tmp" <<EOF
{
  "mcpServers": {
    "collab": {
      "command": $command_json
    }
  }
}
EOF
  fi
  mv "$tmp" "$path"
}

write_codex_config() {
  path=$1
  bin=$2
  mkdir -p "$(dirname "$path")"
  tmp="${path}.tmp.$$"
  if [ -f "$path" ]; then
    awk '
      /^\[mcp_servers\.collab\]$/ { skip = 1; next }
      /^\[/ && skip { skip = 0 }
      !skip { print }
    ' "$path" > "$tmp"
    if [ -s "$tmp" ] && [ "$(tail -c 1 "$tmp")" != "" ]; then
      printf '\n' >> "$tmp"
    fi
  else
    : > "$tmp"
  fi
  printf '[mcp_servers.collab]\ncommand = "%s"\n' "$(printf '%s' "$bin" | sed 's/\\/\\\\/g; s/"/\\"/g')" >> "$tmp"
  mv "$tmp" "$path"
}

write_claude_settings() {
  path=$1
  mkdir -p "$(dirname "$path")"
  tmp="${path}.tmp.$$"
  if [ -f "$path" ]; then
    if command -v jq >/dev/null 2>&1; then
      jq '
        .enabledMcpjsonServers = ((.enabledMcpjsonServers // []) + ["collab"] | unique) |
        .permissions = (.permissions // {}) |
        .permissions.allow = ((.permissions.allow // []) + [
          "mcp__collab__*",
          "Bash(basename:*)",
          "Bash(git status:*)",
          "Bash(git log:*)",
          "Bash(git show:*)",
          "Bash(git diff:*)",
          "Bash(git branch:*)",
          "Bash(git rev-parse:*)",
          "Bash(git worktree list:*)",
          "Bash(git check-ignore:*)",
          "Bash(go build:*)",
          "Bash(go test:*)",
          "Bash(go vet:*)",
          "Bash(gofmt:*)"
        ] | unique)
      ' "$path" > "$tmp"
    elif [ "$force" -eq 1 ]; then
      cat > "$tmp" <<'EOF'
{
  "enabledMcpjsonServers": [
    "collab"
  ],
  "permissions": {
    "allow": [
      "mcp__collab__*",
      "Bash(basename:*)",
      "Bash(git status:*)",
      "Bash(git log:*)",
      "Bash(git show:*)",
      "Bash(git diff:*)",
      "Bash(git branch:*)",
      "Bash(git rev-parse:*)",
      "Bash(git worktree list:*)",
      "Bash(git check-ignore:*)",
      "Bash(go build:*)",
      "Bash(go test:*)",
      "Bash(go vet:*)",
      "Bash(gofmt:*)"
    ]
  }
}
EOF
    else
      die "$path exists and jq is unavailable; install jq or rerun with --force to overwrite it"
    fi
  else
    cat > "$tmp" <<'EOF'
{
  "enabledMcpjsonServers": [
    "collab"
  ],
  "permissions": {
    "allow": [
      "mcp__collab__*",
      "Bash(basename:*)",
      "Bash(git status:*)",
      "Bash(git log:*)",
      "Bash(git show:*)",
      "Bash(git diff:*)",
      "Bash(git branch:*)",
      "Bash(git rev-parse:*)",
      "Bash(git worktree list:*)",
      "Bash(git check-ignore:*)",
      "Bash(go build:*)",
      "Bash(go test:*)",
      "Bash(go vet:*)",
      "Bash(gofmt:*)"
    ]
  }
}
EOF
  fi
  mv "$tmp" "$path"
}

write_agent_doc() {
  path=$1
  agents=$2
  if [ -f "$path" ] && grep -q 'botfam fam member' "$path"; then
    return
  fi
  if [ -f "$path" ]; then
    printf '\n' >> "$path"
  fi
  cat >> "$path" <<EOF
# botfam fam member — read this first

This checkout is one agent's worktree in a botfam coordination fam. Every
agent works in its own worktree of this repo, shares a maildir under
\`~/.botfam/\`, and talks through the \`collab\` MCP server.

## Your name

Your actor name is this worktree's directory basename, with any leading
\`wt-\` or \`botfam-\` stripped:

- \`wt-claude\` -> \`claude\`
- \`wt-codex\` -> \`codex\`
- \`wt-agy\` -> \`agy\`

Configured roster: $agents

If in doubt, run \`basename "\$PWD"\` and apply that rule before your first
collab call.

## Identity rule

On your first \`collab\` tool call, pass \`actor: "<your-name>"\`. The server
binds that name to the session. You may omit \`actor\` on later calls.

## Coordination tools

- Messaging: \`send\`, \`recv\`, \`try_recv\`, \`peek\`, \`ack\`, \`seen\`, \`inbox\`
- Task queue: \`post\`, \`claim\`, \`complete\`, \`heartbeat\`, \`abandon\`, \`sweep\`

\`recv\` blocks cheaply until a message arrives. Delivery is at-least-once:
\`ack(id)\` after you durably handle a message, and use \`seen(id)\` to dedup
when needed.
EOF
}

ensure_botfam_bin() {
  requested=$1
  install_bin=$2
  script_dir=$3

  if [ -n "$requested" ]; then
    [ -x "$requested" ] || die "--botfam-bin is not executable: $requested"
    abs_path "$requested"
    return
  fi

  if command -v botfam >/dev/null 2>&1; then
    command -v botfam
    return
  fi

  if [ -x "$HOME/bin/botfam" ]; then
    abs_path "$HOME/bin/botfam"
    return
  fi

  [ -f "$script_dir/go.mod" ] || die "botfam binary not found; pass --botfam-bin PATH"
  [ -d "$script_dir/cmd/botfam" ] || die "botfam source not found; pass --botfam-bin PATH"
  command -v go >/dev/null 2>&1 || die "Go is required to build botfam; pass --botfam-bin PATH"

  mkdir -p "$(dirname "$install_bin")"
  note "building botfam binary at $install_bin"
  (cd "$script_dir" && go build -o "$install_bin" ./cmd/botfam)
  if [ "$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$install_bin" >/dev/null 2>&1 || note "codesign failed; macOS may block $install_bin"
  fi
  abs_path "$install_bin"
}

repo=
agents=
project=
worktree_dir=
create_worktrees=1
force=0
botfam_bin_arg=
install_bin="${HOME}/bin/botfam"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --agents)
      [ "$#" -gt 1 ] || die "--agents requires a value"
      agents=$2
      shift 2
      ;;
    --agents=*)
      agents=${1#--agents=}
      shift
      ;;
    --project)
      [ "$#" -gt 1 ] || die "--project requires a value"
      project=$2
      shift 2
      ;;
    --project=*)
      project=${1#--project=}
      shift
      ;;
    --worktree-dir)
      [ "$#" -gt 1 ] || die "--worktree-dir requires a value"
      worktree_dir=$2
      shift 2
      ;;
    --worktree-dir=*)
      worktree_dir=${1#--worktree-dir=}
      shift
      ;;
    --no-worktrees)
      create_worktrees=0
      shift
      ;;
    --botfam-bin)
      [ "$#" -gt 1 ] || die "--botfam-bin requires a value"
      botfam_bin_arg=$2
      shift 2
      ;;
    --botfam-bin=*)
      botfam_bin_arg=${1#--botfam-bin=}
      shift
      ;;
    --install-bin)
      [ "$#" -gt 1 ] || die "--install-bin requires a value"
      install_bin=$2
      shift 2
      ;;
    --install-bin=*)
      install_bin=${1#--install-bin=}
      shift
      ;;
    --force)
      force=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      die "unknown option $1"
      ;;
    *)
      if [ -n "$repo" ]; then
        die "unexpected argument $1"
      fi
      repo=$1
      shift
      ;;
  esac
done

[ -n "$repo" ] || die "REPO is required"
[ -n "$agents" ] || die "--agents is required"

repo=$(abs_path "$repo")
[ -d "$repo" ] || die "repo does not exist: $repo"
git -C "$repo" rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not a git repository: $repo"

script_dir=$(cd "$(dirname "$0")" && pwd -P)
botfam_bin=$(ensure_botfam_bin "$botfam_bin_arg" "$install_bin" "$script_dir")
project=${project:-$(basename "$repo")}
worktree_dir=${worktree_dir:-$(dirname "$repo")}
worktree_dir=$(abs_path "$worktree_dir")

setup_args="setup $project --agents $agents"
if [ "$force" -eq 1 ]; then
  setup_args="$setup_args --force"
fi

note "using botfam binary: $botfam_bin"
note "setting up project '$project' for agents: $agents"
# shellcheck disable=SC2086
(cd "$repo" && "$botfam_bin" $setup_args)

all_checkouts=$repo
worktree_summary=
if [ "$create_worktrees" -eq 1 ]; then
  mkdir -p "$worktree_dir"
  for agent in $(split_agents "$agents"); do
    wt="$worktree_dir/wt-$agent"
    branch="agent/$agent"
    if [ -d "$wt" ]; then
      if git -C "$wt" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        note "worktree exists: $wt"
      else
        die "path exists but is not a git worktree: $wt"
      fi
    elif git -C "$repo" show-ref --verify --quiet "refs/heads/$branch"; then
      note "creating worktree $wt on existing branch $branch"
      git -C "$repo" worktree add "$wt" "$branch"
    else
      note "creating worktree $wt on new branch $branch"
      git -C "$repo" worktree add -b "$branch" "$wt" HEAD
    fi
    all_checkouts="$all_checkouts $wt"
    worktree_summary="${worktree_summary}
  - actor: $agent  worktree: $wt  branch: $branch"
  done
fi

for checkout in $all_checkouts; do
  note "writing harness config in $checkout"
  write_json_mcp_config "$checkout/.mcp.json" "$botfam_bin"
  write_json_mcp_config "$checkout/.agents/mcp_config.json" "$botfam_bin"
  write_codex_config "$checkout/.codex/config.toml" "$botfam_bin"
  write_claude_settings "$checkout/.claude/settings.json"
  write_agent_doc "$checkout/AGENTS.md" "$agents"
  if [ ! -f "$checkout/CLAUDE.md" ]; then
    write_agent_doc "$checkout/CLAUDE.md" "$agents"
  fi
done

cat <<EOF

botfam bootstrap complete.

Project:     $project
Repository:  $repo
Agents:      $agents
botfam bin:  $botfam_bin
Worktrees:$worktree_summary

Next steps:
  1. Open each harness in its worktree, for example $worktree_dir/wt-codex.
  2. Restart any already-running harness so MCP config is reloaded.
  3. On the first collab call, pass actor matching the worktree name, e.g. codex.
EOF
