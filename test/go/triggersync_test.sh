#!/bin/sh
# triggersync_test.sh — the chain from a consumer's status context to the comment
# that explains it, exercised on fixtures that break it four different ways.
#
# The chain has three hops and no owner:
#
#   triggers.yaml `context:`  ->  source file  ->  catalog task's `-check`
#
# Lighthouse reports the first as the status a PR shows. The third is what the
# comment's sticky marker carries. Nothing compared them until this tool, which is
# why `audit` can go red on a leartech-dotnet-service-template PR and the PR says
# only "the following test failed" (measured on mikelear/leartech-console-api#31,
# generalised).
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/go/triggersync" || exit 1

command -v go >/dev/null || { echo "SKIP: no go toolchain in this image"; exit 0; }

echo "=== triggersync: status context -> source -> posting name ==="
go test ./... || { echo "FAIL: triggersync's own suite is red"; exit 1; }

out=$(go run . -repo testdata/consumer -catalog testdata/minicatalog 2>&1 || true)
for want in "files its comment under -check mutants" "posts no comment" "reuses context" "does not exist"; do
  case "$out" in
    *"$want"*) echo "  ok   fixture still reports: $want" ;;
    *) echo "FAIL: the fixture no longer reports: $want"; echo "$out" | sed 's/^/    /'; exit 1 ;;
  esac
done

# The clean presubmit must stay quiet, or every real run drowns in noise.
case "$out" in
  *"presubmit test:"*) echo "FAIL: the clean presubmit is reported; the tool over-fires"; exit 1 ;;
  *) echo "  ok   the clean presubmit stays quiet" ;;
esac

echo "=== ok ==="
