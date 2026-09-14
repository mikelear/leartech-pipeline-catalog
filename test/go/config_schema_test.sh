#!/bin/sh
set -eu

# Asserts the shared base config is schema-valid to golangci-lint itself.
#
# This test used to PREPEND `version: "2"` before checking, on the stated
# grounds that "base alone cannot be verified: version is supplied by the
# consuming repo". That assumption was the defect. lint-config has a second
# path -- "no local .golangci.yml; using base as-is" -- which copies the base
# straight to .golangci.merged.yml and supplies nothing. Synthesising the key
# meant this test verified the merged path twice and the as-is path never,
# while golangci-lint v2 rejected the real thing:
#
#   Error: can't load config: unsupported version of the configuration: ""
#
# So the base now declares its own version, and BOTH paths are checked below.
# The as-is case is first: it is the one that was broken.

root=$(cd "$(dirname "$0")/../.." && pwd)
base="$root/go/.golangci.base.yml"

command -v golangci-lint >/dev/null 2>&1 || { echo "SKIP: golangci-lint not on PATH"; exit 0; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

rc=0

# PATH 1 -- base used AS-IS, which is what `make lint-config` does for a repo
# with no local .golangci.yml. Nothing supplies version here, so the file must
# carry it. This is the path that silently could not lint.
echo "  as-is: $(wc -l < "$base" | tr -d ' ') lines"
if out=$(golangci-lint config verify --config "$base" 2>&1); then
  echo "  ok   base loads as-is (the no-local-.golangci.yml path)"
else
  echo "  FAIL base does not load as-is -- a new repo following the documented"
  echo "       path gets a config golangci-lint refuses, while lint-config still"
  echo "       prints 'using base as-is' as though it worked."
  echo "$out" | sed 's/^/         /'
  rc=1
fi

# PATH 2 -- base merged with a consumer's local file, which also declares
# version. This image has no yq, so the merge is emulated: STRIP the base's
# own version line, then prepend the consumer's. That is what yq's ireduce
# does for a duplicated key -- last document wins, one key survives.
#
# Naive `printf version; cat base` is NOT equivalent and must not be used
# here. It concatenates, so both keys survive and viper fails with
# "mapping key version already defined" -- an artefact of the harness, not of
# the merge. That false failure is most likely what the base config's old
# "yq merge + viper unmarshal can choke" NOTE was actually describing.
#
# Verified against the real thing on 2026-09-14: yq eval-all ireduce over
# base+local produced byte-identical 185-line output whether or not the base
# declared version, and golangci-lint loaded both.
grep -v '^version:' "$base" > "$tmp/base-noversion.yml"
{ printf 'version: "2"\n'; cat "$tmp/base-noversion.yml"; } > "$tmp/merged.yml"
if out=$(golangci-lint config verify --config "$tmp/merged.yml" 2>&1); then
  echo "  ok   base still loads when a consumer also declares version"
else
  echo "  FAIL declaring version in the base broke the merged path -- which is"
  echo "       what the old NOTE in the base config warned about."
  echo "$out" | sed 's/^/         /'
  rc=1
fi

if [ "$rc" = "0" ]; then
  echo "PASS: the shared base config is schema-valid on both paths"
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
