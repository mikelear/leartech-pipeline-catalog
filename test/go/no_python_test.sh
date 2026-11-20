#!/bin/sh
# no_python_test.sh — the catalog does not gain python, and the python it has shrinks.
#
# WHY THIS EXISTS. `python3` was removed from this estate's CI for a measured reason, and
# the rule is already enforced for CONSUMER packs: go/e2e/contract_check.go fails a pack
# script that calls python3 — 47 scripts across 18 repos did, and leartech-console-api#24
# read a missing interpreter as "auth refusals broken", a security check failing about
# correct code.
#
# The catalog's own side was never checked, and it has three problems, all measured:
#
#   1. Six test/go/*.sh guards parse YAML in a python heredoc, and the `test` presubmit
#      runs in golang:1.27.4 — no python3, no pyyaml — so each prints "SKIP: no pyyaml in
#      this image" and exits 0. On catalog#356's own pipeline run, four skipped. The
#      guards that verify the comment contract, the message ratchet and task syntax do
#      not run where they exist to run.
#   2. tasks/kyverno-audit/pullrequest.yaml carries `python3 - <<'PYEOF'`, and
#      tasks/ai-review/pullrequest.yaml carries `python3 -c`. A python heredoc inside a
#      step's `script: |` is the pattern that broke rendering for three days in May, and
#      docs/end2end-preview.md bans it in consumer packs. kyverno-audit's step image is
#      distroless (`/bin/sh` does not exist), so that step cannot start at all.
#   3. Four *.py files are tracked, from a text-splicing exercise this catalog's own
#      history records as producing six unparseable files.
#
# WHAT IT DOES NOT FLAG: `python3 /app/foo.py` inside someone else's image, and
# tasks/python*/. The catalog cannot read a script shipped in an image (the same
# distinction as test/go/delegated_posters.txt), and a Python service's step runs
# pip/pytest by definition.
#
# THE RATCHET. Everything above is in test/go/python_baseline.txt, one line per file.
# A file not in the baseline may not carry python; a baseline line whose file no longer
# does must be deleted in the same PR. Both halves are failures, so the list can only
# shrink by someone converting a file.
set -u
cd "$(dirname "$0")/../.." || exit 1

BASELINE=test/go/python_baseline.txt
SELF=test/go/no_python_test.sh
fail=0
note() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

echo "=== python in the catalog (ratchet) ==="

# A file carries python when a non-comment line invokes it, or when it runs a heredoc from
# it. Comments are skipped deliberately: a checker that flags its own documentation is the
# "counts mentions" defect dual_payload's history records.
carries_python() {
  awk '
    # Documentation is not a call site. `#` is shell; `//` and `*` are the Go comments
    # inside the embedded authtoken program, whose whole subject is the python it
    # replaced — flagging that would be the "counts mentions" defect.
    /^[[:space:]]*(#|\/\/|\*)/ { next }
    # `python3 -c ...` and a heredoc fed to python: code written inline in a step.
    /python3?[[:space:]]+-c/ { found = 1 }
    /python3?[[:space:]].*<</ { found = 1 }
    # `python3 foo.py` (relative) is catalog code. `python3 /app/x.py` runs a script
    # shipped INSIDE someone elses image, which this repo cannot read or change — the
    # same distinction test/go/delegated_posters.txt draws.
    /python3?[[:space:]]+[^-]/ {
      rest = $0
      sub(/.*python3?[[:space:]]+/, "", rest)
      if (rest !~ /^\//) found = 1
    }
    END { exit !found }
  ' "$1"
}

listed() {
  grep -v '^#' "$BASELINE" 2>/dev/null | grep -v '^$' | cut -d'|' -f1
}

# ── P1: nothing outside the baseline gains python ────────────────────────────────
scanned=0
for f in test/go/*.sh end2end-runner/*.sh tasks/*/*.yaml tasks/*/*/*.yaml; do
  [ -f "$f" ] || continue
  [ "$f" = "$SELF" ] && continue   # this file names python3 in its own messages
  case "$f" in tasks/python*|tasks/*/python*) continue ;; esac
  scanned=$((scanned + 1))
  carries_python "$f" || continue
  if ! listed | grep -qx "$f"; then
    note "$f carries python3 and is not in $BASELINE."
    case "$f" in
      tasks/*)
        printf '       Inline python in a step script is the pattern that broke rendering\n'
        printf '       for three days in May. Parsing belongs in the Go tool that owns the\n'
        printf '       data (chartstd, prpost, e2e), which is testable where the step is not.\n' ;;
      *)
        printf '       The testing has to be runnable where it runs (golang:1.27.4 has no\n'
        printf '       python3/pyyaml) and testable the way the rest of the estate is (Go).\n' ;;
    esac
    printf '       Add it to the Go tool it is testing, or record it in the baseline with\n'
    printf '       the reason it cannot be Go yet.\n'
  fi
done

# ── P2: a baseline line whose file converted is stale, and must go ───────────────
#
# Without this the list is a permanent licence: convert a file, leave the line, and the
# next reader believes nine files still need python.
# A here-doc, NOT `listed | while read`: a pipe puts the loop in a SUBSHELL, so `fail`
# is incremented where nobody can read it and the check exits 0 having printed FAIL.
# (The same brace-vs-paren bug this estate already paid for once.)
while read -r f; do
  [ -n "$f" ] || continue
  if [ ! -f "$f" ]; then
    note "$f is listed in $BASELINE but does not exist (delete the line)"
  elif ! carries_python "$f"; then
    note "$f no longer carries python3 but is still listed in $BASELINE"
    printf '       Delete its line: the baseline is a ratchet, and a stale line reads as debt.\n'
  fi
done <<EOF
$(listed)
EOF

# ── P2b: a tracked *.py file is catalog code, and catalog code is Go ─────────────
#
# Four one-off patch scripts were tracked at the repo root and in go/chartstd/ until
# 2026-11-20: text-splicing scripts, the same exercise this catalog's history records as
# producing six unparseable YAML files in one batch. They are deleted rather than
# baselined, and this rule keeps the next one out.
tracked_py=$(git ls-files '*.py' 2>/dev/null || true)
if [ -n "$tracked_py" ]; then
  while read -r f; do
    [ -n "$f" ] || continue
    if ! listed | grep -qx "$f"; then
      note "$f is tracked Python in the catalog."
      printf '       Catalog tooling is Go: it builds into the checkers image, it is testable\n'
      printf '       with `go test`, and it runs where the pipeline runs. A patch script kept\n'
      printf '       in the tree is a change nobody can re-run or review.\n'
    fi
  done <<EOF
$tracked_py
EOF
fi

# ── P3: the anchored count ───────────────────────────────────────────────────────
#
# A matcher regression reports "0 found, ok" — the defect dual_payload's EXPECTED_MIN was
# added for. Nine files carry python today; if fewer are SEEN, this check is blind, not
# the catalog clean.
#
# 2026-11-20: six test/go/*.sh, two end2end-runner/*.sh, two tasks/*.yaml.
EXPECTED_MIN=4
seen=$(listed | while read -r f; do [ -f "$f" ] && carries_python "$f" && echo "$f"; done | wc -l | tr -d ' ')
if [ "$seen" -lt "$EXPECTED_MIN" ]; then
  note "only $seen file(s) matched, expected at least $EXPECTED_MIN — the matcher is blind,"
  printf '       not the catalog clean. Fix the matcher, or lower this number in the same PR\n'
  printf '       as converting a file (and delete its baseline line).\n'
fi

if [ "$fail" -eq 0 ]; then
  echo "  ok   $scanned file(s) scanned; $seen carry python, all listed and none new"
fi
exit "$fail"
