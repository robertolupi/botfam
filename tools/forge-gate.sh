#!/usr/bin/env bash
#
# forge-gate.sh — define, apply, and lint the go-native ccrep merge gate.
#
# Under go-native, ccrep "quorum" is NOT custom code — it's Gitea/Forgejo branch
# protection. The gate therefore lives in forge *config*, so it needs a linter:
# this script both provisions that config (`apply`) and verifies a live forge
# matches it (`check`), from one shared definition of the gate contract.
#
#   forge-gate.sh check --host URL --repo owner/name --branch NAME [opts]
#   forge-gate.sh apply --host URL --repo owner/name --branch NAME [opts]
#
# Options:
#   --token-file F   token file (default ~/.botfam/token-botfam-claude)
#   --token T        token literal (overrides --token-file; not logged)
#   --approvals N    required independent approvals (default 2)
#   --merge-user U   (apply) restrict merge to this user (repeatable); omit = any maintainer
#
# `check` exits non-zero on any drift (CI-friendly). Nothing secret is printed.
#
# The go-native gate contract (what a correct botfam branch looks like):
#   required_approvals      >= N           (independent; author is auto-excluded)
#   dismiss_stale_approvals  = true         (new commit re-opens the gate)
#   block_on_rejected_reviews= true         (request_changes blocks)
#   enable_push              = false        (no direct pushes — PRs only)
#   enable_status_check      = false        (no leftover custom ccrep-merge-gate)
#   enable_force_push        = false
#
set -euo pipefail
set +x  # never trace — keep the token out of any log

CMD="${1:-}"; shift || true
case "$CMD" in check|apply) ;; *) echo "usage: forge-gate.sh <check|apply> --host URL --repo owner/name --branch NAME [opts]" >&2; exit 2;; esac

HOST=""; REPO=""; BRANCH=""; TOKEN_FILE="$HOME/.botfam/token-botfam-claude"; TOKEN=""
APPROVALS=2; MERGE_USERS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2;;
    --repo) REPO="$2"; shift 2;;
    --branch) BRANCH="$2"; shift 2;;
    --token-file) TOKEN_FILE="$2"; shift 2;;
    --token) TOKEN="$2"; shift 2;;
    --approvals) APPROVALS="$2"; shift 2;;
    --merge-user) MERGE_USERS="${MERGE_USERS:+$MERGE_USERS,}$2"; shift 2;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) echo "error: unknown arg $1" >&2; exit 2;;
  esac
done
[ -n "$HOST" ] && [ -n "$REPO" ] && [ -n "$BRANCH" ] || { echo "error: --host, --repo, --branch are required" >&2; exit 2; }
if [ -z "$TOKEN" ]; then
  [ -r "$TOKEN_FILE" ] || { echo "error: token file $TOKEN_FILE not readable (use --token-file or --token)" >&2; exit 2; }
  TOKEN="$(tr -d '\r\n' < "$TOKEN_FILE")"
fi

CMD="$CMD" HOST="$HOST" REPO="$REPO" BRANCH="$BRANCH" TOKEN="$TOKEN" \
APPROVALS="$APPROVALS" MERGE_USERS="$MERGE_USERS" python3 - <<'PY'
import os, json, sys, urllib.request, urllib.error

host=os.environ["HOST"].rstrip("/"); repo=os.environ["REPO"]; branch=os.environ["BRANCH"]
tok=os.environ["TOKEN"]; approvals=int(os.environ["APPROVALS"]); cmd=os.environ["CMD"]
merge_users=[u for u in os.environ.get("MERGE_USERS","").split(",") if u]
base=f"{host}/api/v1/repos/{repo}"

def req(method, path, body=None):
    data=json.dumps(body).encode() if body is not None else None
    r=urllib.request.Request(base+path, data=data, method=method,
        headers={"Authorization":"token "+tok,"Content-Type":"application/json"})
    try:
        with urllib.request.urlopen(r) as resp:
            b=resp.read(); return resp.status,(json.loads(b) if b else None)
    except urllib.error.HTTPError as e:
        return e.code,(e.read().decode() or "")

# Desired go-native gate.
desired={
  "required_approvals":approvals,
  "dismiss_stale_approvals":True,
  "block_on_rejected_reviews":True,
  "enable_push":False,
  "enable_status_check":False,
  "enable_force_push":False,
  "block_admin_merge_override":True,
}

if cmd=="apply":
    st,existing=req("GET",f"/branch_protections/{branch}")
    body=dict(desired)
    if merge_users:
        body["enable_merge_whitelist"]=True; body["merge_whitelist_usernames"]=merge_users
    if st==200:
        st2,res=req("PATCH",f"/branch_protections/{branch}",body)
        print(f"apply: PATCH {repo}@{branch} -> HTTP {st2}")
    else:
        body["branch_name"]=branch
        st2,res=req("POST","/branch_protections",body)
        print(f"apply: POST {repo}@{branch} -> HTTP {st2}")
    sys.exit(0 if st2 in (200,201) else 1)

# check
st,bp=req("GET",f"/branch_protections/{branch}")
if st!=200:
    print(f"FAIL: no branch protection on {repo}@{branch} (HTTP {st}) — the gate is missing")
    sys.exit(1)

checks=[]; warns=[]
def chk(name, ok, got, want):
    checks.append((ok,name,got,want))

chk("required_approvals", bp.get("required_approvals",0)>=approvals, bp.get("required_approvals"), f">={approvals}")
chk("dismiss_stale_approvals", bp.get("dismiss_stale_approvals") is True, bp.get("dismiss_stale_approvals"), True)
chk("block_on_rejected_reviews", bp.get("block_on_rejected_reviews") is True, bp.get("block_on_rejected_reviews"), True)
chk("enable_push (direct push blocked)", bp.get("enable_push") is False, bp.get("enable_push"), False)
chk("force-push disabled", bp.get("enable_force_push") is not True, bp.get("enable_force_push"), "not enabled")
# block_admin_merge_override is gitea>=1.x; older Forgejo doesn't report it. Enforce
# where supported, warn (don't fail) where the forge omits it.
_bam = bp.get("block_admin_merge_override")
if _bam is None:
    warns.append("block_admin_merge_override not reported by this forge (older Forgejo?) — admins may bypass quorum; verify manually or upgrade")
else:
    chk("block_admin_merge_override (no admin bypass)", _bam is True, _bam, True)
# go-native: no leftover custom status check (the deleted ccrep-merge-gate)
ctx=bp.get("status_check_contexts") or []
no_legacy = (bp.get("enable_status_check") is not True) or ("ccrep-merge-gate" not in ctx)
chk("no legacy ccrep-merge-gate status check", no_legacy, {"enable_status_check":bp.get("enable_status_check"),"contexts":ctx}, "no ccrep-merge-gate")

ok_all=all(c[0] for c in checks)
print(f"forge-gate check: {repo}@{branch}")
for ok,name,got,want in checks:
    print(f"  [{'PASS' if ok else 'FAIL'}] {name}: got {got!r}, want {want!r}")
for w in warns:
    print(f"  [WARN] {w}")
print("RESULT:", "PASS — go-native gate is correctly configured" if ok_all else "FAIL — gate config drifted")
sys.exit(0 if ok_all else 1)
PY