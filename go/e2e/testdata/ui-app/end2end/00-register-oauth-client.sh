#!/usr/bin/env bash
# Setup: register the OAuth client the specs log in with. Idempotent.
set -uo pipefail
BASE_URL="${STAGING_URL:-${PREVIEW_URL:-}}"
curl -sS -X POST "${BASE_URL%/}/../hydra/clients" -o /dev/null -w '%{http_code}\n' |
  grep -qE '^(200|201|409)$'
