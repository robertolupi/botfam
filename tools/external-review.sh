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
#   tools/external-review.sh --pr <index> [options]   # review a Gitea PR directly
#
# With --pr <index> the material is synthesized from the Gitea PR (metadata,
# description, discussion comments, reviews, and the unified diff), resolved
# from the active git remote; identity/token come from lib-botfam.sh. Output
# dir is slugged pr-<index>. Otherwise MATERIAL is one or more local files.
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
PR=""

while [ $# -gt 0 ]; do
  case "$1" in
    --pr) PR="$2"; shift 2;;
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

[ ${#materials[@]} -gt 0 ] || [ -n "$PR" ] || { echo "error: no material file(s) and no --pr <index> (see --help)" >&2; exit 2; }
[ $(( ${#ollama_models[@]} + ${#gemini_models[@]} + ${#openai_models[@]} )) -gt 0 ] \
  || { echo "error: no models selected — pass at least one --ollama/--gemini/--openai (see --help)" >&2; exit 2; }
[ -f "$PROMPT_FILE" ] || { echo "error: prompt file not found: $PROMPT_FILE" >&2; exit 2; }
command -v jq >/dev/null || { echo "error: jq is required" >&2; exit 2; }
command -v curl >/dev/null || { echo "error: curl is required" >&2; exit 2; }
for f in ${materials[@]+"${materials[@]}"}; do
  [ -f "$f" ] || { echo "error: material not found: $f" >&2; exit 2; }
done

ts="$(date +%Y%m%d-%H%M%S)"
if [ -n "$PR" ]; then
  slug="pr-$PR"
else
  slug="$(basename "${materials[0]}")"; slug="${slug%.*}"
  slug="$(printf '%s' "$slug" | tr -cs 'a-zA-Z0-9' '-')"; slug="${slug#-}"; slug="${slug%-}"
fi
[ -n "$OUT" ] || OUT="${BOTFAM_REVIEW_DIR:-$HOME/.botfam/reviews}/${ts}-${slug}"
mkdir -p "$OUT"

# --pr mode: synthesize the material from a Gitea PR (metadata + description +
# discussion + reviews + unified diff), resolved from the active git remote.
if [ -n "$PR" ]; then
  # shellcheck source=lib-botfam.sh
  . "$(cd "$(dirname "$0")" && pwd)/lib-botfam.sh"; derive_identity
  [ -r "$TOKEN_FILE" ] || { echo "error: token file $TOKEN_FILE not readable (need forge auth for --pr)" >&2; exit 2; }
  pr_remote="$(git config --get remote.gitea.url 2>/dev/null || git config --get remote.origin.url 2>/dev/null || true)"
  [ -n "$pr_remote" ] || { echo "error: no remote.gitea.url/remote.origin.url to resolve the forge" >&2; exit 2; }
  pr_file="$OUT/pr-$PR.md"
  PR="$PR" REMOTE="$pr_remote" PR_TOKEN="$(tr -d '\r\n' < "$TOKEN_FILE")" python3 - "$pr_file" <<'PY'
import os, sys, json, re, urllib.request, urllib.error
remote=os.environ["REMOTE"].strip(); tok=os.environ["PR_TOKEN"]; pr=os.environ["PR"]; out_path=sys.argv[1]
r=remote[:-4] if remote.endswith(".git") else remote
m=re.match(r'^(https?)://([^/]+)/(.+)/([^/]+)$', r)
if m:
    base=f"{m.group(1)}://{m.group(2)}"; owner=m.group(3).split('/')[-1]; repo=m.group(4)
else:
    m=re.match(r'^(?:ssh://)?(?:[^@]+@)?([^:/]+)[:/](.+)/([^/]+)$', r)
    if not m: sys.exit("error: cannot parse git remote %r" % remote)
    base=f"http://{m.group(1)}:3000"; owner=m.group(2).split('/')[-1]; repo=m.group(3)
def get(path, raw=False):
    req=urllib.request.Request(base+"/api/v1"+path, headers={"Authorization":"token "+tok})
    try:
        with urllib.request.urlopen(req) as resp:
            b=resp.read(); return b.decode() if raw else json.loads(b)
    except urllib.error.HTTPError as e:
        sys.exit(f"error: {path} -> HTTP {e.code}: {e.read().decode()[:200]}")
info=get(f"/repos/{owner}/{repo}/pulls/{pr}")
diff=get(f"/repos/{owner}/{repo}/pulls/{pr}.diff", raw=True)
reviews=get(f"/repos/{owner}/{repo}/pulls/{pr}/reviews")
comments=get(f"/repos/{owner}/{repo}/issues/{pr}/comments")
L=[]
L.append(f"# PR #{pr}: {info.get('title','')}\n")
L.append(f"- Repo: {owner}/{repo}\n- Author: {(info.get('user') or {}).get('login')}\n"
         f"- {info.get('head',{}).get('ref')} → {info.get('base',{}).get('ref')}\n- State: {info.get('state')}\n")
L.append("\n## Description\n\n" + ((info.get('body') or '').strip() or "_(no description)_") + "\n")
L.append(f"\n## Discussion ({len(comments)} comment(s))\n")
for c in comments:
    b=(c.get('body') or '').strip()
    if b: L.append(f"\n**{(c.get('user') or {}).get('login')}**: {b}\n")
L.append(f"\n## Reviews ({len(reviews)})\n")
for rv in reviews:
    b=(rv.get('body') or '').strip()
    L.append(f"\n**{(rv.get('user') or {}).get('login')}** [{rv.get('state')}]: {b}\n")
L.append("\n## Unified diff\n\n```diff\n" + diff + "\n```\n")
open(out_path,"w").write("".join(L))
PY
  materials=("$pr_file")
  echo "assembled PR #$PR material: $(wc -c < "$pr_file" | tr -d ' ') bytes -> $pr_file"
fi

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
  [ ${#ollama_models[@]} -eq 0 ] && return
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
