#!/usr/bin/env bash
set -uo pipefail
BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
[ -n "$BASE_URL" ] || { echo "[smoke] no base URL" >&2; exit 2; }
code=$(curl -sS -o /dev/null -w '%{http_code}' -m 10 "${BASE_URL}/" || echo 000)
[ "$code" = "200" ] || { echo "[smoke] GET / -> ${code}"; exit 1; }
echo "[smoke] GET / -> 200 ok"
