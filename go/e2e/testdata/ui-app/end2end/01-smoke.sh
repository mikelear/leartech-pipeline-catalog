#!/usr/bin/env bash
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
code=$(curl -sS -o /dev/null -w '%{http_code}' -m 10 "${BASE_URL}/health" || echo 000)
[ "$code" = "200" ] || { echo "[smoke] ${BASE_URL}/health -> ${code}"; exit 1; }
