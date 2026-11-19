#!/bin/sh
# chartstd_runs_test.sh — a task that RENDERS a chart must judge it.
#
# WHY. leartech-ship-proven-umbrella 0.0.130 shipped a tenant GSA email and
# `gsmProject: product-first` to every customer. S6 forbade the annotation and
# would have caught it — except every umbrella's PR pipeline is tasks/helm, and
# that task ran `helm lint`, `helm template` and the repo's own tests, and never
# once invoked chartstd. The rule existed, was documented, was believed, and had
# never executed on the repos closest to a customer.
#
# So the invariant is not "the standard has the right rules" (the fixtures cover
# that) — it is "the tasks that touch a chart actually run the standard". That is
# a property of the CATALOG, which is why it is checked here and not in a chart.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root" || exit 1

BASELINE=test/go/chartstd_runs_baseline.txt

echo "=== every task that renders a chart also judges it ==="

python3 - "$BASELINE" <<'PY' || exit 1
import glob
import sys

try:
    import yaml
except ImportError:
    print('SKIP: no pyyaml in this image')
    sys.exit(0)

baseline_path = sys.argv[1]
try:
    baseline = {l.strip() for l in open(baseline_path) if l.strip() and not l.startswith('#')}
except FileNotFoundError:
    baseline = set()

renders_without_judging = []
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    src = open(f).read()
    if 'helm template' not in src:
        continue
    if 'chartstd' in src:
        continue
    renders_without_judging.append(f)

print(f'  {len(renders_without_judging)} task(s) run `helm template` and never call chartstd')
print()

new = [f for f in renders_without_judging if f not in baseline]
fixed = [f for f in sorted(baseline) if f not in renders_without_judging]

if fixed:
    print('  NOTE these now judge what they render — delete their line from the baseline:')
    for f in fixed:
        print(f'       {f}')

if new:
    print('FAIL these tasks render a chart and judge nothing:')
    for f in new:
        print(f'       {f}')
    print()
    print('Add a chartstd step (see tasks/helm/pullrequest.yaml), or record why the task')
    print('renders without judging in')
    print(f'  {baseline_path}')
    sys.exit(1)

if fixed:
    sys.exit(1)

print('  ok   every rendering task judges what it renders')
PY

echo "=== ok ==="
