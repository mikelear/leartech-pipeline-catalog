#!/bin/sh
# parity_test.sh — assert `leartech-go.mk lint-config` produces the same
# enabled-linter set that the Tekton go-lint task produces.
#
# This is a REFACTOR guard, not a rule change: the mk file and the task
# should merge base + local in the same way. If someone adds or removes a
# linter in go/.golangci.base.yml without an intentional review, this test
# fails and the PR author has to acknowledge the change.
#
# Runs in a shell environment with `yq` (mikefarah/yq v4) on PATH.
# The pipeline-catalog lint.yaml step image is cytopia/yamllint (no yq),
# so the test invokes yq via a container OR expects it to be preinstalled.
# The catalog test harness runs shell tests via `sh` locally / in the
# yq container; the parity check itself is pure yq + diff.
#
# Exits non-zero on:
#   - lint-config can't produce the merged file
#   - enabled-linter set differs from the golden snapshot
#   - enabled-formatter set differs from the golden snapshot
#
# When the snapshot needs an intentional update (adding a new linter,
# retiring one), update test/go/golden-enabled-linters.txt in the same PR
# as the base config edit.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
GOLDEN_LINTERS="$SCRIPT_DIR/golden-enabled-linters.txt"
GOLDEN_FORMATTERS="$SCRIPT_DIR/golden-enabled-formatters.txt"
WORK=$(mktemp -d /tmp/leartech-go-parity-XXXXXX)
trap 'rm -rf "$WORK"' EXIT

echo "==> parity_test.sh: verify leartech-go.mk merge matches golden snapshots"

if ! command -v yq >/dev/null 2>&1; then
  echo "FAIL: yq (mikefarah/yq v4) required on PATH"
  exit 1
fi

if ! command -v make >/dev/null 2>&1; then
  echo "FAIL: make required on PATH"
  exit 1
fi

# The scenarios we care about:
#   (a) NO local .golangci.yml — base-only merge
#   (b) local .golangci.yml with ONLY the conventional goimports override —
#       shouldn't add or remove any linters from the base set
#
# Both scenarios MUST produce the same enabled linter/formatter set.

run_merge() {
  scenario="$1"
  scenario_dir="$WORK/$scenario"
  mkdir -p "$scenario_dir"
  # Populate the scenario workspace
  case "$scenario" in
    base-only)
      : ;;  # no local .golangci.yml
    with-local-goimports)
      cat >"$scenario_dir/.golangci.yml" <<'EOF'
# Typical consumer override: just the goimports local-prefixes.
version: "2"
formatters:
  settings:
    goimports:
      local-prefixes: github.com/mikelear/example
EOF
      ;;
    *)
      echo "unknown scenario: $scenario"
      exit 1
      ;;
  esac
  # Run lint-config using the LOCAL base file (dogfood the mk).
  # GOLANGCI_MERGED writes into the scenario workspace so scenarios don't
  # collide.
  (
    cd "$scenario_dir"
    make -f "$REPO_ROOT/go/leartech-go.mk" lint-config \
      GOLANGCI_BASE_FILE="$REPO_ROOT/go/.golangci.base.yml" \
      GOLANGCI_MERGED="$scenario_dir/.golangci.merged.yml" \
      >"$scenario_dir/merge.log" 2>&1
  ) || {
    echo "FAIL: lint-config failed for scenario=$scenario"
    cat "$scenario_dir/merge.log"
    exit 1
  }

  # Extract the sorted enabled-linter set + enabled-formatter set from the
  # merged config.
  yq eval '.linters.enable[]' "$scenario_dir/.golangci.merged.yml" \
    | sort >"$scenario_dir/linters.txt"
  # `.formatters.enable` may be absent under some merges — yq prints "null"
  # in that case, which we filter out.
  yq eval '.formatters.enable[]' "$scenario_dir/.golangci.merged.yml" 2>/dev/null \
    | grep -v '^null$' \
    | sort >"$scenario_dir/formatters.txt" || true
}

run_merge base-only
run_merge with-local-goimports

# Both scenarios must agree with each other (base + narrow local override
# must NOT change the linter set).
if ! diff -u "$WORK/base-only/linters.txt" "$WORK/with-local-goimports/linters.txt"; then
  echo "FAIL: local goimports override altered the merged linter set (should not)"
  exit 1
fi
if ! diff -u "$WORK/base-only/formatters.txt" "$WORK/with-local-goimports/formatters.txt"; then
  echo "FAIL: local goimports override altered the merged formatter set (should not)"
  exit 1
fi

# And both scenarios must match the golden snapshot.
if ! diff -u "$GOLDEN_LINTERS" "$WORK/base-only/linters.txt"; then
  echo "FAIL: enabled-linter set differs from golden $GOLDEN_LINTERS"
  echo "      If this change is intentional, update the golden file in the same PR."
  exit 1
fi
if ! diff -u "$GOLDEN_FORMATTERS" "$WORK/base-only/formatters.txt"; then
  echo "FAIL: enabled-formatter set differs from golden $GOLDEN_FORMATTERS"
  echo "      If this change is intentional, update the golden file in the same PR."
  exit 1
fi

echo "PASS: merged linter set + formatter set match golden snapshots"
echo "      linters:    $(wc -l < "$GOLDEN_LINTERS") enabled"
echo "      formatters: $(wc -l < "$GOLDEN_FORMATTERS") enabled"
