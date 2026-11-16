#!/usr/bin/env bash
set -uo pipefail
BASE_URL="${PREVIEW_URL:-}"
body=$(curl -sS "${BASE_URL}/health")
field=$(echo "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')
[ -n "$field" ] || exit 1
