#!/usr/bin/env bash
# THE end2end runner. One copy, for every repo.
#
# The runner's logic — what an exit code MEANS (pass / skip-77 / fail), the
# name stripping, the log tails, the empty/skip/green matrix, the doc-fragment
# metadata — lives in go/e2erun, pinned by its Go tests. This script is the
# executor seam: it resolves the binary (built into the leartech-checkers
# image; /workspace/bin on the pipeline) and runs it against the repo's pack
# directory. The verdict rules are tested once, in Go, not re-implemented here.
set -eo pipefail
SCRIPT_DIR="$(cd "${1:-end2end}" && pwd)"
cd "$SCRIPT_DIR/.."

echo "[end2end] preview=${PREVIEW_URL:-<unset>} ns=${PREVIEW_NAMESPACE:-<unset>} pr=${PULL_NUMBER:-<unset>}"

BIN="$(command -v e2erun || true)"
[ -z "$BIN" ] && [ -x /workspace/bin/e2erun ] && BIN=/workspace/bin/e2erun
if [ -z "$BIN" ]; then
  echo "FAIL: e2erun not found (PATH or /workspace/bin). The checkers image ships it; the pipeline's provide-e2erun step stages it." >&2
  exit 127
fi

exec "$BIN" "$SCRIPT_DIR"
