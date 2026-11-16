#!/usr/bin/env bash
set -uo pipefail
BASE="${PREVIEW_URL:-}"
active_after=$(curl -s -m 20 "${BASE}/api/auth/admin/users/x" | jq -r '.active // ""')
[ "$active_after" = "false" ] || { echo "active=false -> got ${active_after}"; exit 1; }
