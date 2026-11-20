#!/bin/sh
# build_rc_guard_test.sh — the steps after `go build` skip on its EXIT CODE, not
# on the existence of its record file.
#
# `go build` writes /workspace/.go-build.rc on every run, with 0 on success and
# the exit code on failure (it carries onError: continue so `report` can post
# the reason). The image build and the preview promote are guarded on it so a
# broken build does not ship an image or a preview.
#
# Both guards were written as `[ -f /workspace/.go-build.rc ] && exit 0` — true
# on EVERY run, because the file is written on every run. Measured on
# mikelear/leartech-artifact-api#47: `build-container-build` and
# `promote-jx-preview` both exited 0 in under a minute with no output, kaniko
# did not run, no preview namespace appeared, and `gcr/pr` still reported success.
# A green check that deploys nothing is the failure mode this test exists for.

set -u
cd "$(dirname "$0")/../.." || exit 1

TASK=tasks/go/pullrequest.yaml
fail=0
note() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

echo "=== go build rc guards ==="

if [ ! -f "$TASK" ]; then
  note "$TASK is missing"
  exit 1
fi

# Check the build step still records the code the guards read.
if ! grep -q '> /workspace/.go-build.rc' "$TASK"; then
  note "$TASK no longer records the go build exit code in .go-build.rc"
fi

# No step may skip on the file merely existing.
if grep -nE '\[ -[ef] /workspace/\.go-build\.rc \][^\n]*&& *exit +0' "$TASK"; then
  note "a step still skips on the EXISTENCE of .go-build.rc (true on every run)"
fi

# Check that every skip site reads the value.
guards=$(grep -c 'go_build_rc=$(cat /workspace/.go-build.rc' "$TASK")
if [ "$guards" -lt 2 ]; then
  note "expected both downstream steps to read the rc value; found $guards"
fi

# And compare it against 0.
comparisons=$(grep -c '\[ "$go_build_rc" != "0" \]' "$TASK")
if [ "$comparisons" -lt 2 ]; then
  note "expected both downstream steps to compare the rc against 0; found $comparisons"
fi

if [ "$fail" -eq 0 ]; then
  echo "  ok: $guards guard(s) read the recorded exit code"
fi
exit "$fail"
