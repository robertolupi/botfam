#!/usr/bin/env bash
#
# external-review.sh — fan a canonical review prompt + material across one or
# more models (local ollama and/or API providers), saving each raw review
# out-of-repo. A later `botfam review` command can wrap this same interface.
#
# Models are chosen entirely via args (no baked-in defaults) — the operator
# knows the current/best model names; this script does not.
#
# Secrets: GEMINI_API_KEY / OPENAI_API_KEY are read from the environment ONLY,
# passed to curl via headers, and NEVER printed or written to any file. The
# script runs `set +x` so a stray trace cannot leak them. Do not change that.
#
# Usage:
#   tools/external-review.sh [options] MATERIAL [MATERIAL...]
#
# Provider/model selection (repeatable — pass as many as you like):
#   --ollama MODEL     run a local ollama model        e.g. --ollama qwen3.5:35b
#   --gemini MODEL     run a Gemini model (needs GEMINI_API_KEY)
#   --openai MODEL     run an OpenAI model (needs OPENAI_API_KEY)
#
# Options:
#   --prompt FILE      canonical prompt (default: doc/review/EXTERNAL-REVIEW-PROMPT.md);
#                      text BELOW the "PROMPT BEGINS BELOW THIS LINE" marker is used.
#   --out DIR          output dir (default: ${BOTFAM_REVIEW_DIR:-$HOME/.botfam/reviews}/<ts>-<slug>)
#   --ollama-host URL  default: http://localhost:11434
#   --gemini-api-version V   default: v1beta
#   -h | --help
#
# Output: one review-<provider>-<model>.md per model + MANIFEST.txt + the
# combined prompt, all under the out dir. Raw reviews are kept OUT of the repo
# on purpose; consolidate them with a subagent rather than reading them into the
# main context.
#
set -euo pipefail
set +x  # never trace: keeps API keys out of any log

PROMPT_FILE="doc/review/EXTERNAL-REVIEW-PROMPT.md"
OUT=""
OLLAMA_HOST="${OLLAMA_HOST:-http://localhost:11434}"
GEMINI_API_VERSION="v1beta"
ollama_models=()
gemini_models=()
openai_models=()
materials=()

while [ $# -gt 0 ]; do
  case "$1" in
    --ollama) ollama_models+=("$2"); shift 2;;
    --gemini) gemini_models+=("$2"); shift 2;;
    --openai) openai_models+=("$2"); shift 2;;
    --prompt) PROMPT_FILE="$2"; shift 2;;
    --out) OUT="$2"; shift 2;;
    --ollama-host) OLLAMA_HOST="$2"; shift 2;;
    --gemini-api-version) GEMINI_API_VERSION="$2"; shift 2;;
    -h|--help) sed -n '2,40p' "$0"; exit 0;;
    --) shift; while [ $# -gt 0 ]; do materials+=("$1"); shift; done;;
    -*) echo "error: unknown option $1" >&2; exit 2;;
    *) materials+=("$1"); shift;;
  esac
done

[ ${#materials[@]} -gt 0 ] || { echo "error: no material file(s) given (see --help)" >&2; exit 2; }
[ $(( ${#ollama_models[@]} + ${#gemini_models[@]} + ${#openai_models[@]} )) -gt 0 ] \
  || { echo "error: no models selected — pass at least one --ollama/--gemini/--openai (see --help)" >&2; exit 2; }
[ -f "$PROMPT_FILE" ] || { echo "error: prompt file not found: $PROMPT_FILE" >&2; exit 2; }
command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "error: curl is required" >&2; exit 2; }
for f in "${materials[@]}"; do
  [ -f "$f" ] || { echo "error: material not found: $f" >&2; exit 2; }
done

ts="$(date +%Y%m%d-%H%M%S)"
slug="$(basename "${materials[0]}")"; slug="${slug%.*}"
slug="$(printf '%s' "$slug" | tr -cs 'a-zA-Z0-9' '-')"; slug="${slug#-}"; slug="${slug%-}"
[ -n "$OUT" ] || OUT="${BOTFAM_REVIEW_DIR:-$HOME/.botfam/reviews}/${ts}-${slug}"
mkdir -p "$OUT"

# Combined prompt = canonical prompt (below marker) + the material(s).
combined="$OUT/combined-prompt.txt"
{
  awk 'p; /PROMPT BEGINS BELOW THIS LINE/{p=1}' "$PROMPT_FILE"
  printf '\n\n## Material under review\n\n'
  for f in "${materials[@]}"; do
    printf '### %s\n\n' "$f"
    cat "$f"
    printf '\n\n'
  done
} > "$combined"
echo "combined prompt: $(wc -c < "$combined" | tr -d ' ') bytes -> $combined"

ran=()
sanitize() { printf '%s' "$1" | tr -cs 'a-zA-Z0-9' '_'; }

run_ollama() {
  local m out req
  curl -fsS "$OLLAMA_HOST/api/tags" >/dev/null 2>&1 \
    || { echo "  ollama not reachable at $OLLAMA_HOST — skipping ${#ollama_models[@]} local model(s)" >&2; return; }
  for m in "${ollama_models[@]}"; do
    out="$OUT/review-ollama-$(sanitize "$m").md"
    req="$(mktemp)"
    jq -n --arg model "$m" --rawfile p "$combined" '{model:$model, prompt:$p, stream:false}' > "$req"
    echo "  ollama: $m ..."
    if curl -fsS "$OLLAMA_HOST/api/generate" -H 'Content-Type: application/json' -d @"$req" \
         | jq -r '.response // .error // "(no response)"' > "$out" 2>/dev/null; then
      ran+=("ollama:$m"); echo "    -> $out"
    else echo "    FAILED (ollama:$m)" >&2; fi
    rm -f "$req"
  done
}

run_gemini() {
  [ ${#gemini_models[@]} -eq 0 ] && return
  [ -n "${GEMINI_API_KEY:-}" ] || { echo "  GEMINI_API_KEY unset — skipping ${#gemini_models[@]} Gemini model(s)" >&2; return; }
  local m out req
  for m in "${gemini_models[@]}"; do
    out="$OUT/review-gemini-$(sanitize "$m").md"
    req="$(mktemp)"
    jq -n --rawfile p "$combined" '{contents:[{parts:[{text:$p}]}]}' > "$req"
    echo "  gemini: $m ..."
    if curl -fsS "https://generativelanguage.googleapis.com/${GEMINI_API_VERSION}/models/${m}:generateContent" \
         -H "x-goog-api-key: ${GEMINI_API_KEY}" -H 'Content-Type: application/json' -d @"$req" \
         | jq -r '.candidates[0].content.parts[].text // .error.message // "(no response)"' > "$out" 2>/dev/null; then
      ran+=("gemini:$m"); echo "    -> $out"
    else echo "    FAILED (gemini:$m)" >&2; fi
    rm -f "$req"
  done
}

run_openai() {
  [ ${#openai_models[@]} -eq 0 ] && return
  [ -n "${OPENAI_API_KEY:-}" ] || { echo "  OPENAI_API_KEY unset — skipping ${#openai_models[@]} OpenAI model(s)" >&2; return; }
  local m out req
  for m in "${openai_models[@]}"; do
    out="$OUT/review-openai-$(sanitize "$m").md"
    req="$(mktemp)"
    jq -n --arg model "$m" --rawfile p "$combined" '{model:$model, messages:[{role:"user",content:$p}]}' > "$req"
    echo "  openai: $m ..."
    if curl -fsS https://api.openai.com/v1/chat/completions \
         -H "Authorization: Bearer ${OPENAI_API_KEY}" -H 'Content-Type: application/json' -d @"$req" \
         | jq -r '.choices[0].message.content // .error.message // "(no response)"' > "$out" 2>/dev/null; then
      ran+=("openai:$m"); echo "    -> $out"
    else echo "    FAILED (openai:$m)" >&2; fi
    rm -f "$req"
  done
}

echo "running reviews into $OUT ..."
run_ollama
run_gemini
run_openai

{
  echo "timestamp: $ts"
  echo "prompt: $PROMPT_FILE"
  echo "material:"; for f in "${materials[@]}"; do echo "  - $f"; done
  echo "models:"; for r in ${ran[@]+"${ran[@]}"}; do echo "  - $r"; done
} > "$OUT/MANIFEST.txt"

echo
echo "wrote ${#ran[@]} review(s) to: $OUT"
echo "NEXT: spawn a consolidation subagent on this dir; do NOT read the raw reviews into the main context."
