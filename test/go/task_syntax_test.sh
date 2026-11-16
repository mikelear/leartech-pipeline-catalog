#!/bin/sh
# task_syntax_test.sh — every step script in the catalog must PARSE.
#
# WHY. Three task steps have never been valid shell: the
# `if [ -n "$GIT_TOKEN" ] && [ -n "$PULL_NUMBER" ]; then` block that posts the sticky
# comment was opened and never closed in dotnet-test, rust-test and dotnet-mutants.
# A parse error kills the step before it runs a line, so the visible symptom is
# "no coverage comment" or a bare failed step — never "the script is broken".
#
# Then two broke in a different way on 2026-11-16: moving the poster out of a
# `( cd /tmp/pp && go run … )` subshell left the closing paren behind, in four more
# files. Same class: a script that cannot start, reporting as a gate that failed.
#
# `bash -n` is the cheapest check there is, and it catches both. It does not execute
# anything, needs no cluster, and takes about a second for the whole catalog.
#
# Runs in the catalogue's own test presubmit, whose image is golang — so this script
# uses python3 for YAML and bash for the check, and nothing else.

set -u
cd "$(dirname "$0")/../.." || exit 1

echo "=== every task step script parses ==="

python3 - <<'PY' || exit 1
import glob
import os
import subprocess
import sys
import tempfile

try:
    import yaml
except ImportError:
    print('SKIP: no pyyaml in this image')
    sys.exit(0)

bad = []
count = 0
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    doc = yaml.safe_load(open(f))
    tasks = (doc.get('spec') or {}).get('pipelineSpec', {}).get('tasks') or []
    for t in tasks:
        for s in (t.get('taskSpec') or {}).get('steps') or []:
            script = s.get('script')
            if not script:
                continue
            count += 1
            fd, path = tempfile.mkstemp(suffix='.sh')
            with os.fdopen(fd, 'w') as fh:
                fh.write(script)
            r = subprocess.run(['bash', '-n', path], capture_output=True, text=True)
            os.unlink(path)
            if r.returncode != 0:
                first = (r.stderr.strip().splitlines() or ['(no message)'])[0]
                bad.append((f, s.get('name') or '(unnamed)', first))

print(f'  parsed {count} step script(s)')
if bad:
    print()
    for f, name, err in bad:
        print(f'  FAIL {f} :: step {name}')
        print(f'       {err}')
    print()
    print('A step whose script does not parse never runs: the gate fails with no')
    print('explanation of its own, and the only symptom is an absent result. Fix the')
    print('script; do not relax this check.')
    raise SystemExit(1)
print('  ok   every step script parses')
PY

echo "=== ok ==="
