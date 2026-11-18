#!/bin/sh
# failure_report_test.sh — a task that can fail must say why, on the PR.
#
# WHY. Measured on mikelear/leartech-console-api#31 (2026-11-18): gcp/lint failed
# on comment-gate and the PR carried exactly one message about it — Lighthouse's
# generic
#
#   @mikelear: The following test **failed**, say `/retest` to rerun them all
#
# with no cause. The cause existed and was excellent (`+16 prose comment line(s)
# and +0 test line(s)`, three claims with file:line each); it was in a log nobody
# was told to open. A person watching the PR, and an agent that waits for a first
# failure, both learn "something is red" and nothing else.
#
# The existing guards all check the SHAPE of a report that exists — dual_payload
# checks a prpost call carries -outcome/-check/-summary, message_layer counts prose
# in one. Nothing asked whether a task reports at all, which is why 12 tasks could
# exit 1 and post nothing.
#
# This is a ratchet, for the same reason message_layer is one: a test that simply
# failed would red the catalog on day one and be disabled by the end of the week.
# The baseline may only SHRINK. A task is fixed by giving it a report that runs on
# its failure branch; its line is deleted in the same PR.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root" || exit 1

BASELINE=test/go/failure_report_baseline.txt

python3 - "$BASELINE" <<'PY' || exit 1
import glob
import re
import sys

baseline_path = sys.argv[1]
try:
    baseline = {
        l.strip()
        for l in open(baseline_path)
        if l.strip() and not l.startswith('#')
    }
except FileNotFoundError:
    baseline = set()

# A task "can fail" if a step script exits non-zero. A task "reports" if it calls
# one of the two posting tools AND does so on a path a failure can reach — a
# `prpost` inside an `if [ success ]` branch does not count, so the check looks
# for the tool name anywhere in the file and leaves the branch analysis to the
# person fixing it, whose PR will show the comment or not.
# A task can fail two ways, and the narrow definition only saw the first:
#
#   explicit  `exit 1` — a branch the author wrote
#   implicit  `set -e` and a command that fails — how MOST tasks fail
#
# tasks/dotnet and tasks/dotnet-lint fail from their build command under `set -e`,
# so they were invisible to this guard while triggersync correctly reported them.
FAILS = re.compile(r'^\s*exit\s+[1-9]', re.M)
IMPLICIT = re.compile(r'^\s*set\s+-[a-z]*e', re.M)
REPORTS = re.compile(r'\b(prpost|post_sticky|post_report)\b')

explicit_silent = []
implicit_silent = []
total = 0
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    src = open(f).read()
    if 'kind: PipelineRun' not in src:
        continue
    total += 1
    explicit = bool(FAILS.search(src))
    implicit = bool(IMPLICIT.search(src))
    if REPORTS.search(src):
        continue
    if explicit:
        explicit_silent.append(f)
    elif implicit:
        implicit_silent.append(f)

print(f'  {len(explicit_silent)} of {total} task(s) exit explicitly and post nothing '
      f'(the ratchet below)')
if implicit_silent:
    # An inventory, never a failure. 28 tasks fail from a command under `set -e`
    # with no literal `exit 1`, and pretending that is not the same problem would
    # be worse than the narrow rule it replaces. Failing on all 28 at once would
    # make the ratchet a cliff nobody climbs.
    print(f'  {len(implicit_silent)} more fail IMPLICITLY (set -e, no literal exit) and '
          f'post nothing — same symptom, not yet ratcheted:')
    for f in implicit_silent:
        print(f'       {f}')
print()

new_ = [f for f in explicit_silent if f not in baseline]
fixed = [f for f in sorted(baseline) if f not in explicit_silent]

if fixed:
    print('  NOTE these now report — delete their line from the baseline:')
    for f in fixed:
        print(f'       {f}')

if new_:
    print('FAIL the set of silent failures grew:')
    for f in new_:
        print(f'       {f}')
    print()
    print('A step that fails without telling the PR is the shape that made')
    print('console-api#31 unreadable: the reason was in the log, the PR said')
    print('"the following test failed". Give the task a report on its failure')
    print('branch (see tasks/go-lint/pullrequest.yaml), or record why it cannot')
    print('have one by adding its line to')
    print(f'  {baseline_path}')
    sys.exit(1)

if fixed:
    sys.exit(1)

print('  ok   no new task fails without reporting it')
PY

echo "=== ok ==="
