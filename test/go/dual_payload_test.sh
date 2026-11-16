#!/bin/sh
# dual_payload_test.sh — every comment this catalog posts must carry BOTH halves.
#
# WHY. Each message is two payloads in one comment: markdown for a human, and a fenced
# ```leartech-report block for an agent. That is the contract that lets a reader see the
# verdict and a machine act on it, and it is why `prpost` exists at all.
#
# It is also easy to break in a way nothing notices: a `prpost` invocation missing
# `-outcome` still posts (defaulting to success), missing `-check` still posts (against the
# wrong context), and missing `-metrics` still posts (with `metrics: {}`, so an agent gets
# a verdict with no numbers). None of those fail the step. The comment appears, it is just
# quietly wrong — the same class as every other defect in this catalog.
#
# So: for every step that posts, assert the invocation carries the fields the two payloads
# are made of. A step is only compliant if a human and an agent both get something true.

set -u
cd "$(dirname "$0")/../.." || exit 1

echo "=== every posted comment carries a human half and a machine half ==="

python3 - <<'PY' || exit 1
import glob
import re
import sys

try:
    import yaml
except ImportError:
    print('SKIP: no pyyaml in this image')
    sys.exit(0)

# -tool and -check identify the post and its sticky key; -outcome and -summary are the
# machine block's verdict and the human's headline; -cluster separates the two clusters'
# comments; -body-file is the human markdown itself.
REQUIRED = ['-tool', '-check', '-outcome', '-summary', '-cluster', '-body-file']

posted = 0
bad = 0
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    doc = yaml.safe_load(open(f))
    for t in (doc.get('spec') or {}).get('pipelineSpec', {}).get('tasks') or []:
        for s in (t.get('taskSpec') or {}).get('steps') or []:
            script = s.get('script') or ''
            for m in re.finditer(r'/workspace/bin/prpost(?P<tail>\s+-tool(?:[^\n]*\\\n)*[^\n]*)', script):
                tail = m.group('tail')
                posted += 1
                missing = [flag for flag in REQUIRED if flag not in tail]
                if missing:
                    bad += 1
                    print(f'  FAIL {f} :: step {s.get("name") or "(unnamed)"}')
                    print(f'       missing {", ".join(missing)}')
                    print(f'       the comment would still post, with a defaulted '
                          f'{"verdict" if "-outcome" in missing else "field"} — '
                          f'a payload that looks complete and is not')

print(f'  {posted} prpost invocation(s) checked')

# The machine half has to exist in the renderer, not just be requested by the caller.
proto = open('go/prpost/main.go').read()
if 'leartech-report' not in proto:
    print('  FAIL go/prpost does not emit the ```leartech-report block; the machine '
          'half of the contract is gone and every comment is now human-only')
    bad += 1
# The footer is the CALLER's (each task names itself), so what matters here is that the
# post sites still write one: a comment with no attribution cannot be traced to a task.
footers = sum(1 for f in glob.glob('tasks/**/*.yaml', recursive=True)
              if '_Posted by' in open(f).read())
if footers == 0:
    print('  WARNING no task writes a "_Posted by" footer; a reader cannot tell which '
          'task spoke')

if bad:
    print()
    print('A comment is a contract with two readers. A field missing here does not fail')
    print('the run — it produces a comment that reads as complete and is wrong.')
    raise SystemExit(1)
print('  ok   every post site supplies both halves')
PY

echo "=== ok ==="
