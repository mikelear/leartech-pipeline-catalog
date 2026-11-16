#!/usr/bin/env bash
# The shared smoke shape: probes answer, and an unauthenticated call is refused.
set -uo pipefail
BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
[ -n "$BASE_URL" ] || { echo "[smoke] no base URL" >&2; exit 2; }
echo "[smoke] base=${BASE_URL}"
check() { # method path want
  code=$(curl -sS -o /dev/null -w '%{http_code}' -m 10 -X "$1" "${BASE_URL}$2" 2>/dev/null || echo 000)
  if [ "$code" = "$3" ]; then echo "[smoke] $1 $2 -> $code ok"; else
    echo "[smoke] $1 $2 -> $code (want $3)"; return 1; fi
}
check GET /health 200
check GET /api/v1/artifacts 401
