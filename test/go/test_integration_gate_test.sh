#!/bin/sh
# test_integration_gate_test.sh — assert `leartech-go.mk test-integration`
# cannot pass without having run anything.
#
# This is a BEHAVIOUR guard on the one property that makes the target worth
# having. The target exists because auth-service's testcontainers harness sat
# unrun by CI for months and ai-gateway grew the same gap: the go-test task
# invokes test-coverage, which passes no -tags, so a tagged file is never
# compiled. A target that silently passed when it could not reach a datastore
# would reproduce that failure with more steps.
#
# Three cases, and the middle one is the point:
#
#   no tagged tests           -> PASS, with a stated reason (nothing to run is
#                                not a failure, and must not be a silent one)
#   tagged tests, no backend  -> FAIL (never skip)
#   tagged tests, TEST_DATABASE_URL set -> attempts the run
#
# Deliberately does NOT start a database. It asserts the gate's decision, not
# that any repo's tests pass — those belong to the consuming repo.
#
# Runs under `sh`. Uses a scratch dir so it cannot be affected by, or affect,
# the catalog's own tree.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
MK="$REPO_ROOT/go/leartech-go.mk"

[ -f "$MK" ] || { echo "FAIL: $MK not found"; exit 1; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
printf 'module scratch\n\ngo 1.24\n' > go.mod

# ── case 1: a repo with no integration-tagged tests passes, and says why ──
out=$(make -f "$MK" test-integration 2>&1) || {
  echo "FAIL case 1: a repo with no tagged tests should PASS"
  echo "$out" | sed 's/^/    /'
  exit 1
}
echo "$out" | grep -q "nothing to run" || {
  echo "FAIL case 1: passed without stating that it ran nothing."
  echo "  A silent pass is indistinguishable from a successful run, which is the"
  echo "  failure this target exists to prevent."
  echo "$out" | sed 's/^/    /'
  exit 1
}
echo "ok   case 1: no tagged tests -> pass, with a reason"

# ── case 2: tagged tests + no backend must FAIL, not skip ────────────────
mkdir -p internal/store
cat > internal/store/thing_integration_test.go <<'GO'
//go:build integration

package store

import "testing"

func TestNeedsADatabase(t *testing.T) {}
GO

# Hide ONLY docker, by shadowing it with a failing stub. An earlier draft
# blanked PATH entirely, which hid `make` as well — so the target never ran and
# the test "failed" on env: make: No such file or directory while reporting that
# the diagnosis was missing. A negative that fires for the wrong reason is worse
# than no test: it was green-adjacent noise pointing at the wrong line.
mkdir -p "$work/stub"
cat > "$work/stub/docker" <<'STUB'
#!/bin/sh
exit 1
STUB
chmod +x "$work/stub/docker"

if out=$(env PATH="$work/stub:$PATH" TEST_DATABASE_URL= \
           make -f "$MK" test-integration 2>&1); then
  echo "FAIL case 2: tagged tests with no reachable datastore PASSED."
  echo "  It must fail. A green tick from an unconfigured run says the opposite"
  echo "  of what it means (end2end/run.sh states the same rule)."
  echo "$out" | sed 's/^/    /'
  exit 1
fi
echo "$out" | grep -q "no datastore is reachable" || {
  echo "FAIL case 2: it failed, but not with the diagnosis that explains why."
  echo "  A failure that does not name the missing backend sends the reader to"
  echo "  the test code instead of to colima."
  echo "$out" | sed 's/^/    /'
  exit 1
}
echo "ok   case 2: tagged tests + no backend -> fail, with a diagnosis"

# ── case 3: the tagged file must actually be DETECTED ────────────────────
# Guards the detection regex rather than the run. If detection silently missed
# the file, case 2 would pass for the wrong reason — it would be taking the
# "nothing to run" branch, which is case 1 wearing case 2's clothes.
out=$(env PATH="$work/stub:$PATH" make -f "$MK" test-integration 2>&1 || true)
echo "$out" | grep -q "thing_integration_test.go" || {
  echo "FAIL case 3: the integration-tagged file was not detected."
  echo "  Case 2 would then be passing via the 'nothing to run' branch and"
  echo "  asserting nothing about the no-backend path."
  echo "$out" | sed 's/^/    /'
  exit 1
}
echo "ok   case 3: the tagged file is detected, so case 2 tested the right branch"

echo "test-integration gate: all cases pass"
