#!/usr/bin/env bash
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
code=$(curl -sS -o /dev/null -w '%{http_code}' "${BASE_URL}/health")
[ "$code" = 200 ] || exit 1
