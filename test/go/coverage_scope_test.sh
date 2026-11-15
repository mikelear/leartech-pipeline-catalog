#!/bin/sh
# coverage_scope_test.sh — a coverage run whose scope matches no packages must
# say that, not report 0.0%.
#
# The bug this pins: `COVERAGE_SCOPE` defaults to ./internal/... and a repo whose
# Go lives at the module root (or in cmd/, or pkg/) produced an empty profile.
# `go tool cover` reports that as "0.0%" and the gate then fails with "below floor
# 60.0%" — which reads as "your code is untested", sends the reader looking at
# tests, and is wrong: nothing was measured. Two repos in this estate sat red for
# that reason (it-test/lh-private and leartech-forge-testbed) before the scope was
# suspected at all.
#
# Needs a Go toolchain (the go-test image has one) and nothing else.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
MK="$REPO_ROOT/go/leartech-go.mk"

command -v go >/dev/null 2>&1 || { echo "SKIP: no go toolchain on PATH"; exit 0; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/mod"
cd "$WORK/mod"
cat > go.mod <<'EOF'
module example.com/scopecheck

go 1.21
EOF
cat > add.go <<'EOF'
package scopecheck

func Add(a, b int) int { return a + b }
EOF
cat > add_test.go <<'EOF'
package scopecheck

import "testing"

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
EOF

fails=0
check() {
  desc=$1; want=$2; shift 2
  out=$(make -f "$MK" test-coverage "$@" 2>&1 || true)
  if printf '%s' "$out" | grep -q "$want"; then
    printf '  ok   %s\n' "$desc"
  else
    printf '  FAIL %s\n       wanted output containing: %s\n' "$desc" "$want"
    printf '%s\n' "$out" | grep -E 'total=|FAIL|matched no packages' | head -3
    fails=$((fails + 1))
  fi
}

echo "=== coverage scope ==="

check "a scope with no packages names the scope, not the floor" \
  "matched no packages" \
  COVERAGE_SCOPE=./internal/... COVERAGE_THRESHOLD=60

check "a scope with no packages does not claim a percentage" \
  "COVERAGE_SCOPE=./internal/... matched no packages" \
  COVERAGE_SCOPE=./internal/... COVERAGE_THRESHOLD=60

check "the real scope reports a real number" \
  "PASS: coverage 100.0% meets threshold 0%" \
  COVERAGE_SCOPE=./... COVERAGE_THRESHOLD=0

if [ "$fails" -ne 0 ]; then
  echo
  echo "FAIL: $fails case(s)."
  exit 1
fi
echo "=== ok ==="
