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

try:
    import yaml
except ImportError:
    print('SKIP: no pyyaml in this image')
    sys.exit(0)

baseline_path = sys.argv[1]
try:
    baseline = {
        l.strip()
        for l in open(baseline_path)
        if l.strip() and not l.startswith('#')
    }
except FileNotFoundError:
    baseline = set()

# Tasks that post from INSIDE their image. No catalog rule can see that, so the
# honest report says "unverified" rather than listing them as silent failures --
# the same distinction triggersync draws with a NOTE. Named with the file that
# does the posting, because "we could not tell" is only useful if it says what.
# Read from test/go/delegated_posters.txt, which dual_payload_test.sh reads too. Two
# copies of this list is the defect both guards exist to catch, one level up.
DELEGATES = {}
for _line in open('test/go/delegated_posters.txt'):
    _line = _line.strip()
    if not _line or _line.startswith('#'):
        continue
    _path, _, _who = _line.partition('|')
    DELEGATES[_path.strip()] = _who.strip()


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
        if f in DELEGATES:
            continue
        implicit_silent.append(f)

print(f'  {len(explicit_silent)} of {total} task(s) exit explicitly and post nothing '
      f'(the ratchet below)')
# The list can only SHRINK by a decision. Without an anchor, deleting a line moves a
# finding back into the silent category and the count simply reads 4 instead of 5 --
# the silent-shrink this whole check exists to catch, one level up.
EXPECTED_DELEGATES = 5
if len(DELEGATES) < EXPECTED_DELEGATES:
    print(f'FAIL only {len(DELEGATES)} delegated poster(s) listed, expected {EXPECTED_DELEGATES}:')
    print('     test/go/delegated_posters.txt')
    print('     A removed entry does not fix a payload; it makes the task look silent.')
    print('     If a poster genuinely moved INTO the catalog, delete its line here and add')
    print('     the prpost rules it now needs -- in the same PR.')
    sys.exit(1)

if DELEGATES:
    print(f'  {len(DELEGATES)} task(s) post from INSIDE their image, so no catalog rule can '
          f'see it — unverified, not silent:')
    for f, who in sorted(DELEGATES.items()):
        print(f'       {f}  ({who})')
    print()

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

# A report step is only reachable if the step it reports on does not stop the
# task. Tekton abandons the remaining steps as soon as one exits non-zero, so a
# task with a report step and no `onError: continue` posts nothing — and reads,
# from the file, exactly like one that works.
bad_onerror = []
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    doc = yaml.safe_load(open(f))
    for t in (doc.get('spec') or {}).get('pipelineSpec', {}).get('tasks') or []:
        steps = (t.get('taskSpec') or {}).get('steps') or []
        if 'report' not in [s.get('name') for s in steps]:
            continue
        if not any(s.get('onError') == 'continue' for s in steps):
            bad_onerror.append(f)

if bad_onerror:
    print('FAIL these tasks have a report step that can never run:')
    for f in sorted(set(bad_onerror)):
        print(f'       {f}')
    print()
    print('Add `onError: continue` to the step the report follows.')
    sys.exit(1)
print('  ok   every report step is reachable')

print('  ok   no new task fails without reporting it')
PY

echo "=== ok ==="
