#!/usr/bin/env bash
#
# chaos.sh — take each dependency down in turn and assert shortn behaves as
# documented in docs/runbook.md. Wired as `make chaos`. Run `make up` first.
#
# It asserts the *contract* (which requests still succeed vs fail when a
# dependency is gone), not the circuit breaker's timing — the breaker's fast-fail
# needs sustained load and is shown manually.
set -uo pipefail

COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
BASE="${BASE:-http://localhost}"
fails=0

# Restore every dependency on exit, even if an assertion fails or the script dies.
cleanup() {
  $COMPOSE unpause redis >/dev/null 2>&1 || true
  $COMPOSE start redpanda postgres >/dev/null 2>&1 || true
}
trap cleanup EXIT

status() { curl -s -o /dev/null -w '%{http_code}' "$1"; }

expect() { # expect <label> <actual> <wanted>
  if [ "$2" = "$3" ]; then
    echo "  PASS  $1 ($2)"
  else
    echo "  FAIL  $1 — got $2, want $3"
    fails=$((fails + 1))
  fi
}

expect_5xx() { # expect_5xx <label> <actual>
  if [ "$2" -ge 500 ] 2>/dev/null; then
    echo "  PASS  $1 ($2)"
  else
    echo "  FAIL  $1 — got $2, want a 5xx"
    fails=$((fails + 1))
  fi
}

echo "== baseline =="
resp=$(curl -s -X POST "$BASE/api/links" -d '{"url":"https://example.com"}')
code=$(echo "$resp" | sed -E 's/.*"code":"([^"]+)".*/\1/')
echo "  warm code: $code"
curl -s -o /dev/null "$BASE/$code" # warm the cache
expect "redirect works" "$(status "$BASE/$code")" "302"
expect "healthz ok" "$(status "$BASE/healthz")" "200"
expect "readyz ok" "$(status "$BASE/readyz")" "200"

echo "== Redis down (cache + limiter) =="
$COMPOSE pause redis >/dev/null
expect "redirect still works (served from Postgres)" "$(status "$BASE/$code")" "302"
expect "healthz stays up" "$(status "$BASE/healthz")" "200"
expect "readyz stays green (Redis non-critical)" "$(status "$BASE/readyz")" "200"
$COMPOSE unpause redis >/dev/null

echo "== Redpanda down (analytics) =="
$COMPOSE stop redpanda >/dev/null
expect "redirect unaffected (publish is non-fatal)" "$(status "$BASE/$code")" "302"
$COMPOSE start redpanda >/dev/null

echo "== Postgres down (source of truth) =="
$COMPOSE stop postgres >/dev/null
sleep 2 # let in-flight connections drop
expect "cache hit still redirects" "$(status "$BASE/$code")" "302"
expect "healthz stays up (no dep check)" "$(status "$BASE/healthz")" "200"
expect "readyz reports not-ready" "$(status "$BASE/readyz")" "503"
expect_5xx "cold read fails" "$(status "$BASE/chaoscold")"
$COMPOSE start postgres >/dev/null

echo
if [ "$fails" -eq 0 ]; then
  echo "CHAOS PASSED — behavior matches docs/runbook.md"
else
  echo "CHAOS FAILED — $fails check(s) off"
  exit 1
fi
