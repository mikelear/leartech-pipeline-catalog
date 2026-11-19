#!/bin/sh
# chartstd_test.sh — the Ship Proven chart standard, run as a guard here.
#
# The store every fixture is judged against is the one this estate supplies from
# config/secrets.yaml; -store is required, because "never name a store in a
# default" can only be judged against the store that is actually supplied.
#
# The checker lives in go/chartstd and judges what a chart RENDERS, because every
# rule in docs/chart-standard.md is about the object the cluster receives. This
# guard proves the checker itself still fires: the fixtures under
# go/chartstd/testdata are shapes this estate has actually shipped, and each one
# must produce exactly the rule it is filed under.
#
# Why a guard and not only `go test`: the catalog's presubmit runs these shell
# files, and a Go test that nobody invokes is the "runner nothing runs" the helm
# task already refuses. If the Go suite ever stops being wired up, this line goes
# red instead of quiet.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root/go/chartstd"

command -v go >/dev/null || { echo "SKIP: no go toolchain in this image"; exit 0; }

echo "=== chartstd: the chart standard (S1b, S2, S3, S4, S5, S6, S8, T1, T2) ==="
go test ./... || { echo "FAIL: chartstd's own suite is red"; exit 1; }

# The five shapes, one line each, so a regression names the rule that stopped
# firing rather than "a test failed".
check() {
  dir=$1; want=$2
  out=$(go run . -store leartech-secrets -render "testdata/$dir/render.yaml" 2>&1 || true)
  case "$out" in
    *"$want"*) echo "  ok   $dir -> $want" ;;
    *) echo "FAIL: $dir no longer produces $want"; echo "$out" | sed 's/^/    /'; exit 1 ;;
  esac
}

check clean ""
check console-api-default-store S1
check empty-store S1b
check double-claim S3
check data-and-template S4
check workload-identity S6

# S8 needs the templates, so it needs the chart directory as well as the render.
out=$(go run . -store leartech-secrets -render testdata/bool-default/render.yaml \
  -chart testdata/bool-default -templates 2>&1 || true)
case "$out" in
  *S8*) echo "  ok   bool-default -> S8" ;;
  *) echo "FAIL: bool-default no longer produces S8"; echo "$out" | sed 's/^/    /'; exit 1 ;;
esac

# S5 is a source rule, so it needs the chart directory as well.
out=$(go run . -store leartech-secrets -render testdata/todo-password/render.yaml -chart testdata/todo-password 2>&1 || true)
case "$out" in
  *S5*) echo "  ok   todo-password -> S5" ;;
  *) echo "FAIL: todo-password no longer produces S5"; echo "$out" | sed 's/^/    /'; exit 1 ;;
esac

# T1/T2: a declared off-switch must actually remove the object. toggle-dead is
# console-api#32 verbatim; toggle-stale is a declaration nothing satisfies.
toggle_case() {
  dir=$1; want=$2
  out=$(go run . -store leartech-secrets \
    -render "testdata/$dir/base.yaml" \
    -toggles "testdata/$dir/toggles.yaml" \
    -render-off "testdata/$dir/off.yaml" 2>&1 || true)
  case "$out" in
    *"$want"*) echo "  ok   $dir -> $want" ;;
    *) echo "FAIL: $dir no longer produces $want"; echo "$out" | sed 's/^/    /'; exit 1 ;;
  esac
}

toggle_case toggle-dead T2
toggle_case toggle-stale T1

# And the working chart must stay quiet, or the check cries wolf on the charts
# that are correct.
out=$(go run . -store leartech-secrets -render testdata/toggle-works/base.yaml \
  -toggles testdata/toggle-works/toggles.yaml \
  -render-off testdata/toggle-works/off.yaml 2>&1 || true)
case "$out" in
  *"T1 testdata/"*|*"T2 testdata/"*)
    echo "FAIL: toggle-works reports a toggle finding"; echo "$out" | sed 's/^/    /'; exit 1 ;;
  *) echo "  ok   toggle-works stays quiet" ;;
esac

# S10: a released chart carries no tenant-specific default. tenant-gsa and
# tenant-project are the umbrella's two shipped defects; tenant-clean holds
# estate furniture (our repo refs, our FQDN) that must NOT be flagged.
tenant_case() {
  dir=$1; want=$2
  out=$(go run . -tenant "testdata/$dir" 2>&1 || true)
  case "$out" in
    *FAIL*)
      if [ "$want" = "fail" ]; then
        echo "  ok   $dir -> S10"
      else
        echo "FAIL: $dir should be clean"; echo "$out" | sed 's/^/    /'; exit 1
      fi
      ;;
    *)
      if [ "$want" = "clean" ]; then
        echo "  ok   $dir stays clean"
      else
        echo "FAIL: $dir no longer reports S10"; exit 1
      fi
      ;;
  esac
}

tenant_case tenant-gsa fail
tenant_case tenant-project fail
tenant_case tenant-clean clean

echo "=== ok ==="
