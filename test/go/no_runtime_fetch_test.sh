#!/bin/sh
# no_runtime_fetch_test.sh — the catalog's own tools must arrive pinned, not fetched.
#
# Ten task files used to curl prpost, leartech-go.mk and the rust base deny.toml from
# raw.githubusercontent INSIDE the step that then used them. Three costs, all paid:
#
#   the CDN serves cache-control: max-age=300, so a merged fix was not what ran for
#   up to five minutes — on 2026-09-15 a lint fix looked like it had not worked for
#   exactly that reason;
#
#   the fetcher needs a Go toolchain in whatever image the step uses, which is why a
#   non-Go repo's gate could not be pre-flighted locally;
#
#   every run of every PR re-downloaded and, for prpost, re-compiled the same code.
#
# They now ship in ghcr.io/mikelear/leartech-checkers and are copied in by a
# provide-catalog-tools step. This test fails if either half of that comes back, or if
# the provide step drifts out of order — because a provide step after the step that
# uses it is the same bug with the fetch moved rather than removed.
#
# Checks are line-based on purpose: this runs in the `test` presubmit's golang image,
# which has no PyYAML, and a test that cannot start is not a test.

set -u
cd "$(dirname "$0")/../.." || exit 1

fail=0
note() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

echo "=== no runtime fetch of the catalog's own tools ==="

for f in tasks/*/*.yaml tasks/*/*/*.yaml; do
  [ -f "$f" ] || continue

  if grep -q 'raw.githubusercontent.com/mikelear/leartech-pipeline-catalog' "$f"; then
    note "$f still fetches from the catalog's raw CDN (ship it in the checkers image):"
    grep -n 'raw.githubusercontent.com/mikelear/leartech-pipeline-catalog' "$f" | sed 's/^/       /'
  fi

  if grep -q 'go run prpost' "$f"; then
    note "$f still builds prpost at run time; use /workspace/bin/prpost"
    grep -n 'go run prpost' "$f" | sed 's/^/       /'
  fi

  # Every step that uses a provided tool needs the provide step, and needs it earlier.
  for tool in /workspace/bin/prpost /workspace/bin/leartech-go.mk /workspace/bin/deny.base.toml; do
    grep -q -- "$tool" "$f" || continue
    if ! grep -q 'name: provide-catalog-tools' "$f"; then
      note "$f uses $tool but has no provide-catalog-tools step"
      continue
    fi
    provide_line=$(grep -n 'name: provide-catalog-tools' "$f" | head -1 | cut -d: -f1)
    first_use=$(grep -n -- "$tool" "$f" | grep -v 'provided\|missing from\|cp /usr/local' \
                | head -1 | cut -d: -f1)
    if [ -n "$first_use" ] && [ -n "$provide_line" ] && [ "$first_use" -lt "$provide_line" ]; then
      note "$f uses $tool at line $first_use, before the provide step at line $provide_line"
    fi
  done
done

# A LOCAL provided tool must be read with cp, never curl: curl rejects a bare
# path ("no host part in the URL"), which broke go-lint estate-wide on 2026-11-16
# when the fetch was moved to the image but the curl was left. A fetch form that
# fails is still a fetch bug.
for f in tasks/*/pullrequest.yaml tasks/*/release.yaml; do
  [ -f "$f" ] || continue
  if grep -E 'curl ' "$f" | grep -Eq 'MK_URL|/workspace/bin/leartech-go\.mk'; then
    note "$f curls a LOCAL path (MK_URL / /workspace/bin/leartech-go.mk) — use cp, not curl"
  fi
done

# The image has to actually carry them, or every consumer fails at run time.
for f in go/prpost/main.go go/leartech-go.mk rust/deny.base.toml; do
  [ -f "$f" ] || note "$f is missing, so the image cannot ship it"
done
grep -q 'COPY go/prpost/' checkers/Dockerfile || note "checkers/Dockerfile does not copy go/prpost"
grep -q 'COPY go/leartech-go.mk' checkers/Dockerfile || note "checkers/Dockerfile does not copy go/leartech-go.mk"
grep -q 'COPY rust/deny.base.toml' checkers/Dockerfile || note "checkers/Dockerfile does not copy rust/deny.base.toml"

if [ "$fail" -ne 0 ]; then
  echo
  echo "FAIL: $fail finding(s)."
  exit 1
fi
echo "=== ok ==="
