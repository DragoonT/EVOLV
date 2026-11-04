#!/usr/bin/env bash
# Boot a built EVOLV binary against a throwaway HOME and exercise the real
# server: health, metrics, assets, pack round-trip, security boundaries and a
# clean SIGTERM drain. This is the same smoke the release pipeline runs against
# the exact binary it is about to publish, so a green release means the
# artifact boots — not just that it compiled.
#
# usage: scripts/smoke.sh /path/to/evolv [port]
set -euo pipefail

BIN="${1:-}"
PORT="${2:-${EVOLV_SMOKE_PORT:-17391}}"

if [[ -z "$BIN" || ! -f "$BIN" ]]; then
  echo "usage: scripts/smoke.sh /path/to/evolv [port]" >&2
  exit 2
fi

HOST="127.0.0.1"
BASE="http://${HOST}:${PORT}"
HOME_DIR="$(mktemp -d)"
LOG="${HOME_DIR}/evolv-smoke.log"
PID=""

cleanup() {
  if [[ -n "$PID" ]] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$HOME_DIR"
}
trap cleanup EXIT

BODY="${HOME_DIR}/body"

# `curl | grep -q` fails under pipefail when grep exits before curl finishes
# writing; fetch into a file and search that instead.
fetch_contains() {
  local url="$1"
  local pattern="$2"
  curl -fsS "$url" -o "$BODY"
  grep -qi -- "$pattern" "$BODY"
}

echo "smoke: booting ${BIN} on ${BASE} with HOME=${HOME_DIR}"
env HOME="$HOME_DIR" DISPLAY="" "$BIN" --port "$PORT" --no-open >"$LOG" 2>&1 &
PID=$!

health=""
for _ in $(seq 1 80); do
  if health="$(curl -fsS "${BASE}/api/health" 2>/dev/null)"; then
    break
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "smoke: server exited before becoming healthy" >&2
    cat "$LOG" >&2
    exit 1
  fi
  sleep 0.25
done

if [[ -z "$health" ]]; then
  echo "smoke: server never answered /api/health" >&2
  cat "$LOG" >&2
  exit 1
fi

printf '%s' "$health" | grep -q '"status":"alive"'
fetch_contains "${BASE}/api/metrics" '^evolv_up 1$'
fetch_contains "${BASE}/api/history?limit=1" '"total":0'
fetch_contains "${BASE}/" '<!doctype html>'
fetch_contains "${BASE}/style.css" '--accent'
fetch_contains "${BASE}/ui/ambient.js" 'ambient-stage'
fetch_contains "${BASE}/swarm" 'swarm mode'
fetch_contains "${BASE}/api/swarm" '"agents"'
fetch_contains "${BASE}/api/swarm/runs" '"runs"'

# The .evolvpack path: export a real archive, inspect it, then import it back.
PACK="${HOME_DIR}/smoke.evolvpack"
curl -fsS "${BASE}/api/pack/export?ledger=1" -o "$PACK"
test -s "$PACK"
curl -fsS -X POST -H 'Content-Type: application/octet-stream' \
  --data-binary "@${PACK}" "${BASE}/api/pack/inspect" -o "$BODY"
grep -q '"manifest"' "$BODY"
curl -fsS -X POST -H 'Content-Type: application/octet-stream' \
  --data-binary "@${PACK}" "${BASE}/api/pack/import?force=1&quarantine=1" -o "$BODY"
grep -q '"manifest"' "$BODY"

# Cross-origin state-changing requests and DNS-rebinding Host headers are
# rejected before any handler runs.
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST \
  -H 'Origin: https://evil.example' \
  -H 'Content-Type: application/json' \
  -d '{"message":"should-not-run"}' \
  "${BASE}/api/chat")"
test "$code" = "403"
code="$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example' \
  "${BASE}/api/health")"
test "$code" = "421"

# CLI surface of the exact binary under test.
"$BIN" --version > "$BODY"
grep -q '^evolv ' "$BODY"
"$BIN" --help > "$BODY"
grep -q 'evolv download' "$BODY"
"$BIN" status --port "$PORT" > "$BODY"
grep -q 'alive' "$BODY"

# SIGTERM must drain and exit cleanly rather than hang or die on the signal.
kill -TERM "$PID"
for _ in $(seq 1 60); do
  kill -0 "$PID" 2>/dev/null || break
  sleep 0.1
done
if kill -0 "$PID" 2>/dev/null; then
  echo "smoke: server did not stop after SIGTERM" >&2
  cat "$LOG" >&2
  exit 1
fi
wait "$PID" 2>/dev/null || true
grep -q 'organism paused' "$LOG"

PID=""
echo "smoke: ok"
