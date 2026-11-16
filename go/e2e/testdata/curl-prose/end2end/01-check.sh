#!/usr/bin/env bash
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
if ! body=$(curl -s -m 20 "${BASE_URL}/health"); then
  echo "01-check: curl failed to reach ${BASE_URL}" >&2
  exit 1
fi
[ -n "$body" ] || { echo "01-check: curl returned an empty body" >&2; exit 1; }
