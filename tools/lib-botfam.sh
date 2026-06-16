#!/usr/bin/env bash
#
# lib-botfam.sh — shared bash utilities for botfam scripts.
# Sourced by scripts under tools/ to avoid duplicating identity resolution logic.

# derive_identity resolves the family slug, actor name, and per-agent token file path.
# It sets both uppercase and lowercase variables to accommodate different script styles:
#   FAM / fam: family name
#   ACTOR / actor: worktree actor name
#   TOKEN_FILE / token_file: path to the agent's token file
derive_identity() {
  # actor: explicit command-line or env, else resolved via botfam whoami, else fallback
  if [ -z "${ACTOR:-}" ]; then
    ACTOR="${BOTFAM_ACTOR:-}"
    if [ -z "$ACTOR" ]; then
      if command -v botfam >/dev/null 2>&1 && ACTOR="$(botfam whoami 2>/dev/null)" && [ -n "$ACTOR" ]; then
        :
      elif [ -x "./botfam" ] && ACTOR="$(./botfam whoami 2>/dev/null)" && [ -n "$ACTOR" ]; then
        :
      else
        ACTOR="$(basename "$PWD")"
        ACTOR="${ACTOR#wt-}"
        ACTOR="${ACTOR#botfam-}"
      fi
    fi
  fi
  actor="$ACTOR"

  # fam: explicit command-line or env, else $BOTFAM_FAM, else the worktree's parent dir name
  if [ -z "${FAM:-}" ]; then
    FAM="${BOTFAM_FAM:-$(basename "$(dirname "$PWD")")}"
  fi
  fam="$FAM"

  # token_file: explicit command-line or env, else ~/.botfam/token-<fam>-<actor>
  if [ -z "${TOKEN_FILE:-}" ]; then
    TOKEN_FILE="${BOTFAM_TOKEN_FILE:-$HOME/.botfam/token-${FAM}-${ACTOR}}"
  fi
  token_file="$TOKEN_FILE"
}
