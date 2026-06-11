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

validate_name() {
  kind=$1
  name=$2
  case "$name" in
    '') die "$kind name is empty" ;;
    *[!A-Za-z0-9_-]*) die "invalid $kind name '$name': use only letters, digits, underscore, or dash" ;;
  esac
}

is_git_tracked() {
  checkout=$1
  path=$2
  rel=${path#"$checkout"/}
  git -C "$checkout" ls-files --error-unmatch "$rel" >/dev/null 2>&1
}

write_claude_settings() {
  path=$1
  mkdir -p "$(dirname "$path")"
  tmp="${path}.tmp.$$"
  if [ -f "$path" ]; then
    if command -v jq >/dev/null 2>&1; then
      jq '
        (if .enabledMcpjsonServers then .enabledMcpjsonServers = (.enabledMcpjsonServers - ["collab"]) else . end) |
        .permissions = (.permissions // {}) |
        .permissions.allow = (((.permissions.allow // []) - ["mcp__collab__*"]) + [
          "Bash(botfam:*)",
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
  "permissions": {
    "allow": [
      "Bash(botfam:*)",
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
  "permissions": {
    "allow": [
      "Bash(botfam:*)",
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
  template=$3
  if [ -f "$template" ]; then
    if [ "$(abs_path "$path")" = "$(abs_path "$template")" ]; then
      return
    fi
    if [ -f "$path" ] && grep -q 'botfam fam member' "$path"; then
      cp "$template" "$path"
      return
    fi
    if [ -f "$path" ]; then
      printf '\n' >> "$path"
      cat "$template" >> "$path"
    else
      cp "$template" "$path"
    fi
    return
  fi
  if [ -f "$path" ]; then
    if grep -q 'botfam fam member' "$path"; then
      tmp="${path}.tmp.$$"
      cat > "$tmp" <<EOF
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

## Identity rule (important)

The server binds an actor name to the session — it is sticky and immutable.

- Automatic resolution (recommended): if you run inside a named worktree folder
  such as \`wt-agy\`, the server parses the directory basename to resolve the
  actor as \`agy\`. The family root is derived from repository git history, so
  every worktree and the main checkout share one coordination plane. In this
  case, you do not need to pass \`actor\` on tool calls.
- Explicit naming: alternatively, on your first \`collab\` tool call, pass
  \`actor: "<your-name>"\`. A conflicting \`actor\` is rejected. If no automatic
  resolution is possible and no \`actor\` is provided on the first call, the call
  is refused.

## Coordination tools

- Messaging: \`send\`, \`recv\`, \`try_recv\`, \`peek\`, \`ack\`, \`seen\`, \`inbox\`
- Task queue: \`post\`, \`claim\`, \`complete\`, \`heartbeat\`, \`abandon\`, \`sweep\`

\`recv\` blocks cheaply until a message arrives. Delivery is at-least-once:
\`ack(id)\` after you durably handle a message, and use \`seen(id)\` to dedup
when needed.
EOF
      mv "$tmp" "$path"
      return
    fi
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

## Identity rule (important)

The server binds an actor name to the session — it is sticky and immutable.

- Automatic resolution (recommended): if you run inside a named worktree folder
  such as \`wt-agy\`, the server parses the directory basename to resolve the
  actor as \`agy\`. The family root is derived from repository git history, so
  every worktree and the main checkout share one coordination plane. In this
  case, you do not need to pass \`actor\` on tool calls.
- Explicit naming: alternatively, on your first \`collab\` tool call, pass
  \`actor: "<your-name>"\`. A conflicting \`actor\` is rejected. If no automatic
  resolution is possible and no \`actor\` is provided on the first call, the call
  is refused.

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
  commit_sha=$(git -C "$script_dir" rev-parse HEAD 2>/dev/null || echo "dev")
  (cd "$script_dir" && go build -ldflags "-X github.com/rlupi/botfam/internal/fam.BuildSHA=$commit_sha" -o "$install_bin" ./cmd/botfam)
  if [ "$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$install_bin" >/dev/null 2>&1 || die "codesign failed for $install_bin; macOS may block unsigned MCP binaries"
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
validate_name "project" "$project"
worktree_dir=${worktree_dir:-$(dirname "$repo")}
worktree_dir=$(abs_path "$worktree_dir")

for agent in $(split_agents "$agents"); do
  validate_name "agent" "$agent"
done

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
repo_common=$(cd "$repo" && git rev-parse --path-format=absolute --git-common-dir)
if [ "$create_worktrees" -eq 1 ]; then
  mkdir -p "$worktree_dir"
  for agent in $(split_agents "$agents"); do
    wt="$worktree_dir/wt-$agent"
    branch="agent/$agent"
    if [ -d "$wt" ]; then
      if git -C "$wt" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        wt_common=$(cd "$wt" && git rev-parse --path-format=absolute --git-common-dir)
        [ "$wt_common" = "$repo_common" ] || die "existing worktree $wt does not belong to $repo"
        wt_branch=$(git -C "$wt" branch --show-current)
        [ "$wt_branch" = "$branch" ] || die "existing worktree $wt is on branch $wt_branch, expected $branch"
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
  # Clean up legacy MCP configurations
  rm -f "$checkout/.mcp.json"
  rm -f "$checkout/.agents/mcp_config.json"
  rm -f "$checkout/.codex/config.toml"
  # Delete empty directories if empty
  [ -d "$checkout/.agents" ] && rmdir "$checkout/.agents" 2>/dev/null || true
  [ -d "$checkout/.codex" ] && rmdir "$checkout/.codex" 2>/dev/null || true

  write_claude_settings "$checkout/.claude/settings.json"
  write_agent_doc "$checkout/AGENTS.md" "$agents" "$script_dir/AGENTS.md"
  if [ ! -f "$checkout/CLAUDE.md" ]; then
    write_agent_doc "$checkout/CLAUDE.md" "$agents" "$script_dir/CLAUDE.md"
  fi
  if [ ! -f "$checkout/GEMINI.md" ]; then
    write_agent_doc "$checkout/GEMINI.md" "$agents" "$script_dir/GEMINI.md"
  fi
  if [ -f "$script_dir/doc/collab/PROTOCOL.md" ]; then
    if ! is_git_tracked "$checkout" "$checkout/doc/collab/PROTOCOL.md"; then
      if [ ! -f "$checkout/doc/collab/PROTOCOL.md" ] || ! cmp -s "$script_dir/doc/collab/PROTOCOL.md" "$checkout/doc/collab/PROTOCOL.md"; then
        mkdir -p "$checkout/doc/collab"
        cp "$script_dir/doc/collab/PROTOCOL.md" "$checkout/doc/collab/PROTOCOL.md"
      fi
    fi
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
  2. Run botfam commands directly from the named worktree; actor identity resolves from the
     worktree basename, e.g. wt-codex -> codex.
EOF
