#!/usr/bin/env bash
set -uo pipefail
A_URL="${PREVIEW_URL:-}"
PROBE="/api/v1/example"
code=$(curl -sS -o /dev/null -w '%{http_code}' "${A_URL}${PROBE}")
[ "$code" = 200 ] || { echo "CROSS-SERVICE AUDIENCE BINDING BROKEN"; exit 1; }
