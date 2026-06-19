#!/bin/sh
# End-to-end smoke test for the docker test substrate:
# bring up ergo + scribe, talk IRC from the host, assert scribe tallies.
set -eu
cd "$(dirname "$0")/.."

cleanup() { docker compose -f compose.test.yaml down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== compose up (build) =="
docker compose -f compose.test.yaml up --build -d --wait

echo "== smoke client =="
python3 - <<'EOF'
import socket, sys, time

deadline = time.time() + 45
s = socket.create_connection(("127.0.0.1", 16667), timeout=10)
s.settimeout(5)
f = s.makefile("r", encoding="utf-8", errors="replace")
def send(line): s.sendall((line + "\r\n").encode())

send("NICK smoke"); send("USER smoke 0 * :smoke test client")
registered = joined = False
last_probe = 0.0
while time.time() < deadline:
    try:
        line = f.readline()
    except (TimeoutError, OSError):
        line = ""
    if line:
        line = line.strip()
        if line.startswith("PING"):
            send("PONG " + line.split(" ", 1)[1]); continue
        p = line.split(" ")
        if len(p) > 1 and p[1] == "001":
            registered = True
            send("JOIN #botfam")
        if len(p) > 1 and p[1] == "366":
            joined = True
        if "PRIVMSG #botfam" in line and "version:" in line and ":scribe!" in line:
            print("OK: scribe answered:", line.split(":", 2)[-1])
            send("QUIT"); sys.exit(0)
    # scribe may join after us; re-probe every 3s once we're in the channel
    if joined and time.time() - last_probe > 3:
        send("PRIVMSG #botfam :!version")
        last_probe = time.time()
print("FAIL: no scribe tally reply within deadline "
      f"(registered={registered}, joined={joined})")
sys.exit(1)
EOF
status=$?

echo "== history ledger written by scribe =="
docker compose -f compose.test.yaml exec -T scribe sh -c 'wc -l /data/history.jsonl && tail -2 /data/history.jsonl' || status=1

if [ "$status" -eq 0 ]; then echo "SMOKE TEST PASSED"; else echo "SMOKE TEST FAILED"; fi
exit "$status"
