#!/bin/sh
set -eu

# Asserts the shared base config is schema-valid to golangci-lint itself.
#
# Base alone cannot be verified: `version` is supplied by the consuming repo's
# .golangci.yml. The synthetic local config below is only that key, so
# prepending it is equivalent to the yq deep-merge lint-config performs and
# keeps this runnable in the golangci-lint image (which has no yq).

root=$(cd "$(dirname "$0")/../.." && pwd)
base="$root/go/.golangci.base.yml"

command -v golangci-lint >/dev/null 2>&1 || { echo "SKIP: golangci-lint not on PATH"; exit 0; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

{ printf 'version: "2"\n'; cat "$base"; } > "$tmp/merged.yml"
echo "  base $(wc -l < "$base" | tr -d ' ') lines + version key -> $(wc -l < "$tmp/merged.yml" | tr -d ' ') lines"

if out=$(golangci-lint config verify --config "$tmp/merged.yml" 2>&1); then
  echo "PASS: the shared base config is schema-valid"
  exit 0
fi

echo "FAIL: golangci-lint rejects the shared base config"
echo "$out" | sed 's/^/    /'
echo
echo "  A key in the wrong block is SILENTLY IGNORED by \`golangci-lint run\`, so the"
echo "  declared value never applies and nothing fails. max-issues-per-linter and"
echo "  max-same-issues sat under linters.exclusions this way: the config declared"
echo "  unlimited, the default of 3 applied, and 4 of 7 real findings were hidden."
exit 1
