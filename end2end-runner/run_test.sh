#!/usr/bin/env bash
# Tests for the shared runner, driven with fixture checks.
#
# The runner decides what a check's exit code MEANS — pass, "does not apply
# here", or fail — and writes the results.json the qa-gate reads. It carried
# two defects this year that each survived because nothing exercised it: a
# failing check's log truncated to three lines, and exit 0 used for "skipped"
# so a check that ran nothing was recorded as a pass.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
RUNNER="$PWD/run.sh"

rc=0
fail() { echo "FAIL: $*" >&2; rc=1; }
ok()   { echo "  ok   $*"; }

# fixture <name> <exit-code> <output...> — writes a check that prints and exits
fixture() {
  local dir="$1" name="$2" code="$3"; shift 3
  mkdir -p "$dir"
  { echo '#!/usr/bin/env bash'; for l in "$@"; do echo "echo '$l'"; done; echo "exit $code"; } > "$dir/$name"
  chmod +x "$dir/$name"
}

run_case() { # <dir>
  ( cd "$(dirname "$1")" && bash "$RUNNER" "$(basename "$1")" >/tmp/runner.out 2>&1 )
  echo $?
}

WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT

# ── a passing check ─────────────────────────────────────────────────────────
d="$WORK/pass/end2end"; fixture "$d" "01-green.sh" 0 "all good"
run_case "$d" >/dev/null
grep -q '"status": *"pass"' "$d/results.json" && grep -q '"success": *true' "$d/results.json" \
  && ok "a passing check yields success=true" \
  || fail "a passing check did not produce success=true: $(head -c 200 "$d/results.json")"

# ── a failing check prints its WHOLE log ────────────────────────────────────
# The defect: only the last three lines survived, so a check that explained
# itself line by line had its explanation discarded.
d="$WORK/fail/end2end"; fixture "$d" "01-red.sh" 1 "line one of the diagnosis" "line two" "line three" "line four" "line five"
run_case "$d" >/dev/null
if grep -q "line one of the diagnosis" /tmp/runner.out; then
  ok "a failing check's whole log is printed, not just the tail"
else
  fail "the first line of a failing check's log was not printed — the tail-only
      defect is back, and a check that diagnosed itself is thrown away."
fi
grep -q '"success": *false' "$d/results.json" \
  && ok "a failing check yields success=false" \
  || fail "a failing check did not set success=false"

# ── exit 77 is a skip, not a pass and not a failure ─────────────────────────
d="$WORK/skip/end2end"; fixture "$d" "01-na.sh" 77 "SKIP: needs a preview issuer"
run_case "$d" >/dev/null
if grep -q '"status": *"skip"' "$d/results.json"; then
  ok "exit 77 is recorded as skip"
else
  fail "exit 77 was not recorded as a skip: $(head -c 200 "$d/results.json")"
fi
grep -q "needs a preview issuer" "$d/results.json" \
  && ok "the skip reason travels into results.json" \
  || fail "a skip was recorded with no reason, so it cannot be told from coverage"

# ── a mix: one pass, one skip, one fail ─────────────────────────────────────
d="$WORK/mix/end2end"
fixture "$d" "01-green.sh" 0 "ok"; fixture "$d" "02-na.sh" 77 "SKIP: not here"; fixture "$d" "03-red.sh" 1 "boom"
run_case "$d" >/dev/null
grep -q '"success": *false' "$d/results.json" \
  && ok "one failure among passes and skips still fails the suite" \
  || fail "a suite containing a failure reported success"
grep -q "1 skipped" "$d/results.json" \
  && ok "the summary counts the skip" \
  || fail "the summary did not mention the skip: $(grep -o '\"summary\":[^,]*' "$d/results.json")"

# ── every check skipped: success, but it must SAY nothing ran ───────────────
d="$WORK/allskip/end2end"; fixture "$d" "01-na.sh" 77 "SKIP: preview only"
run_case "$d" >/dev/null
if grep -q '"success": *true' "$d/results.json" && grep -q "nothing applicable ran" "$d/results.json"; then
  ok "an all-skip suite passes AND says nothing applicable ran"
else
  fail "an all-skip suite did not say so. A green tick that means 'verified' and
      'not verified' at once is the ambiguity this estate keeps paying for.
      summary: $(grep -o '\"summary\":[^,]*' "$d/results.json")"
fi

echo
[ "$rc" -eq 0 ] && echo "runner contract holds" || echo "RUNNER CONTRACT BROKEN"
exit "$rc"
