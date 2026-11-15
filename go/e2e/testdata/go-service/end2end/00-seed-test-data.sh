#!/usr/bin/env bash
# Setup, not a check: idempotent (a duplicate is a 409, which is fine).
set -uo pipefail
BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
[ -n "$BASE_URL" ] || { echo "[seed] no base URL" >&2; exit 2; }
curl -sS -X POST "${BASE_URL}/test/fixtures" -o /dev/null -w '%{http_code}\n' | grep -qE '^(200|201|409)$'
