#!/usr/bin/env bash
# The inlined copy in tasks/end2end/pullrequest.yaml must equal run.sh.
#
# Inlining is deliberate: fetching the runner over raw.githubusercontent would
# put every end2end run in the estate behind GitHub's availability. The cost
# of that choice is drift, and this is what pays it — otherwise the pipeline
# executes a runner that passes none of run_test.sh.
#
# Structure is checked as well as content: every line of a YAML literal block
# must carry the block's indentation, INCLUDING the line the closing heredoc
# marker sits on. Getting that wrong emits a marker at column 0, ends the
# scalar early and leaves the task unparseable — which a content-only
# comparison reports as identical.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
TASK=../tasks/end2end/pullrequest.yaml
INDENT='            '

rc=0
python3 - "$TASK" "$INDENT" <<'PY' || rc=1
import sys
task, indent = open(sys.argv[1]).read(), sys.argv[2]
open_m, close_m = "<<'LEARTECH_RUNNER'\n", "LEARTECH_RUNNER"
i = task.find(open_m)
if i < 0:
    print("FAIL: no LEARTECH_RUNNER heredoc in the task. Either the step was removed —"
          " delete this test rather than let it pass on nothing — or the marker changed"
          " and this test is looking at the wrong thing.")
    raise SystemExit(1)
rest = task[i+len(open_m):]
j = rest.find(close_m)
if j < 0:
    print("FAIL: the LEARTECH_RUNNER heredoc is never closed")
    raise SystemExit(1)
block = rest[:j]

for n, line in enumerate(block.split("\n")):
    if line == "" or line.startswith(indent):
        continue
    print(f"FAIL: line {n+1} of the inlined block lacks the block indentation, so the task"
          f" stops being valid YAML there:\n  {line!r}")
    raise SystemExit(1)
if not block.endswith("\n" + indent):
    print("FAIL: the closing LEARTECH_RUNNER marker is not indented into the block, which"
          " ends the YAML scalar early")
    raise SystemExit(1)

inlined = "".join(l[len(indent):] if l.startswith(indent) else l
                  for l in block.splitlines(keepends=True))
want = open("run.sh").read()
if inlined != want:
    g, w = inlined.split("\n"), want.split("\n")
    for k in range(max(len(g), len(w))):
        a = g[k] if k < len(g) else "<missing>"
        b = w[k] if k < len(w) else "<missing>"
        if a != b:
            print(f"FAIL: the inlined copy differs from run.sh at line {k+1}.\n"
                  f"  inlined: {a}\n  run.sh:  {b}\n"
                  "Regenerate it from run.sh; do not edit the YAML by hand.")
            break
    raise SystemExit(1)
print("  ok   the inlined runner matches run.sh, and the block is well-formed YAML")
PY
exit "$rc"
