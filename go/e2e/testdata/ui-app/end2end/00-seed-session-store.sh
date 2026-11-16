#!/usr/bin/env bash
# The one declared exception in this repo: see preview.bespoke for the reason.
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
[ -n "$BASE_URL" ] || { echo "[seed] no PREVIEW_URL" >&2; exit 2; }
curl -sS -o /dev/null -w '%{http_code}\n' "${BASE_URL}/test/session" | grep -qE '^(200|201|409)$'
