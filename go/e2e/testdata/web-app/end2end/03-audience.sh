#!/usr/bin/env bash
# The audience property: 401 with a token minted for someone else, 200 with its own.
# The route comes from preview.authProbe via $AUTH_PROBE, so this script works on a
# service that gates something other than /api/v1/example.
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
PROBE="${AUTH_PROBE:?the end2end task exports AUTH_PROBE from preview.authProbe}"
code=$(curl -sS -o /dev/null -w '%{http_code}' "${BASE_URL}${PROBE}")
[ "$code" = "401" ] || { echo "[audience] ${PROBE} -> ${code}, want 401 unauthenticated"; exit 1; }
