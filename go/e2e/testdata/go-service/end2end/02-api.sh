#!/usr/bin/env bash
# An authenticated read: the seed script has already created the fixture.
set -uo pipefail
BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
TOKEN="${E2E_TOKEN:-}"
[ -n "$TOKEN" ] || { echo "[api] no E2E_TOKEN for this environment — not applicable"; exit 77; }
curl -sS -m 20 -H "Authorization: Bearer ${TOKEN}" "${BASE_URL}/api/v1/artifacts" | grep -q artifacts
