#!/bin/sh
# valuesstd_test.sh — the secrets-manifest standard, run as a guard here.
#
# The checker lives in go/valuesstd and judges the manifest that seeds a cluster's
# secrets plus the script that derives the ones which cannot be seeded. It is a
# shell guard for the same reason the others are: the catalog's presubmit runs
# these files, and a Go test nobody invokes is the "runner nothing runs".
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/go/valuesstd" || exit 1

command -v go >/dev/null || { echo "SKIP: no go toolchain in this image"; exit 0; }

echo "=== valuesstd: a value's class decides who may write it (V1-V4) ==="
go test ./... || { echo "FAIL: valuesstd's own suite is red"; exit 1; }

for pair in clean:"" url-generated:V2 derived-not-derived:V3 unknown-mode:V1 duplicate:V1 no-why:V1; do
  dir=${pair%%:*}; want=${pair#*:}
  out=$(go run . -manifest "testdata/$dir/secrets.yaml" -derive "testdata/$dir/derive.sh" 2>&1 || true)
  if [ -z "$want" ]; then
    case "$out" in
      *FAIL*) echo "FAIL: $dir should be clean"; echo "$out" | sed 's/^/    /'; exit 1 ;;
      *) echo "  ok   $dir stays clean" ;;
    esac
  else
    case "$out" in
      *"$want"*) echo "  ok   $dir -> $want" ;;
      *) echo "FAIL: $dir no longer produces $want"; echo "$out" | sed 's/^/    /'; exit 1 ;;
    esac
  fi
done

echo "=== ok ==="
