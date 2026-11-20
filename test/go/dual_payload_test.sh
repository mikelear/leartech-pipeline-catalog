#!/bin/sh
# dual_payload_test.sh — every comment this catalog posts must carry BOTH halves,
# and every post site must be visible to this check.
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
# WHAT CHANGED (2026-11-18). This guard used to match only `/workspace/bin/prpost`, and
# five call sites call bare `prpost` after the task exports PATH: chart-standard, go-lint
# and three inside go-test/security-scan. Measured on the day: 13 call sites in 10 files,
# **6 checked**. So the two tasks added that day — the ones whose whole point was to report
# — were the ones the guard could not see. A guard that silently skips a call site is worse
# than no guard, because the count it prints reads as coverage.
#
# RULES
#   P1  every call site is seen, bare or pathed (this file is only as good as its match)
#   P2  it carries -tool -check -outcome -summary -cluster -body-file
#   P3  -check equals -tool, or the difference is recorded in the baseline below (an alias
#       is a decision someone made; unrecorded drift is not)
#   P4  a task that posts more than once uses a different -tool each time, because the
#       sticky key is tool+cluster: two sites with one tool overwrite each other's comment
#
# The status CONTEXT is not knowable here — it comes from the consumer repo's triggers.yaml,
# not from the task — so a task whose -check names something no trigger uses is a consumer
# finding, not this one. Recorded in docs/chart-standard.md's kin rather than faked here.

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

REQUIRED = ['-tool', '-check', '-outcome', '-summary', '-cluster', '-body-file']

# P3's allowed differences: task dir -> tool it posts under, where they differ on
# purpose. Each line is a claim that the tool name is chosen for the marker, not the
# task name. Shrink-only: an entry that no longer differs is a stale line.
ALIASES = {
    'dotnet-mutants': 'mutants',
    'dotnet-test': 'coverage',
    'go-test': 'coverage',
    'ng-test': 'coverage',
    'rust-test': 'coverage',
}

# P1: bare `prpost` AND `/workspace/bin/prpost`. The tail must START with a flag, or with a
# variable that expands to flags — an INVOCATION, not a mention. The first draft of this
# widening matched `cp /usr/local/bin/prpost`, `test -x /workspace/bin/prpost` and the text
# of a failure message, and reported 36 sites in 10 files: a guard that counts mentions is
# the same failure it exists to prevent, one level up.
CALL = re.compile(r'(?:^|\s)(?:/workspace/bin/)?prpost\s+(-|"?\$\{)')

posted = 0
bad = 0
sites_by_file = {}

for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    doc = yaml.safe_load(open(f))
    task_dir = f.split('/')[1]
    tools = []
    for t in (doc.get('spec') or {}).get('pipelineSpec', {}).get('tasks') or []:
        for s in (t.get('taskSpec') or {}).get('steps') or []:
            script = s.get('script') or ''
            for m in CALL.finditer(script):
                raw = script[m.end(1) - 1:]
                # A multi-line invocation is one command: join the continuations, THEN
                # take the first line. Splitting first truncated the flags onto lines the
                # check never saw, which read as "missing -summary" on a correct call.
                tail = raw.replace('\\\n', ' ').split('\n')[0]
                posted += 1
                sites_by_file.setdefault(f, 0)
                sites_by_file[f] += 1

                if m.group(1) != '-':
                    # Flags are built into a variable in this script; the builder is the
                    # thing to check, and it must be in the same step.
                    missing = [f for f in ('-tool', '-check', '-outcome') if f not in script]
                else:
                    missing = [flag for flag in REQUIRED if flag not in tail]
                if missing:
                    bad += 1
                    print(f'  FAIL {f} :: step {s.get("name") or "(unnamed)"}')
                    print(f'       missing {", ".join(missing)}')
                    print('       the comment would still post, with a defaulted field — '
                          'a payload that looks complete and is not')
                    continue

                tool = re.search(r'-tool\s+(\S+)', tail)
                check = re.search(r'-check\s+(\S+)', tail)
                if tool:
                    tools.append(tool.group(1))
                if tool and check and tool.group(1) != check.group(1):
                    want = ALIASES.get(task_dir)
                    if want != tool.group(1):
                        bad += 1
                        print(f'  FAIL {f} :: -tool {tool.group(1)} but -check '
                              f'{check.group(1)}, and that difference is not in ALIASES')
                        print('       -tool names the sticky marker, -check names the '
                              'check; an unrecorded difference means one of them is wrong')

    dupes = {x for x in tools if tools.count(x) > 1}
    if dupes:
        bad += 1
        print(f'  FAIL {f} :: posts more than once with -tool {", ".join(sorted(dupes))}')
        print('       the sticky key is tool+cluster, so those sites overwrite one comment')

print(f'  {posted} prpost invocation(s) checked in {len(sites_by_file)} file(s)')

# The population this check can read. A task that posts from INSIDE an image contains no
# prpost call, so it is invisible here and the count above excludes it silently. Named,
# out loud, for the same reason #311 named the failure-report equivalents: a guard that
# reports green over a population it quietly shrinks is the defect it exists to prevent.
DELEGATED = []
with open('test/go/delegated_posters.txt') as fh:
    for line in fh:
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        path, _, who = line.partition('|')
        DELEGATED.append((path.strip(), who.strip()))

if DELEGATED:
    print(f'  {len(DELEGATED)} posting path(s) are OUTSIDE this check -- the comment is built in')
    print('  their image, so its payload (including the leartech-report half) is unverified here:')
    for path, who in DELEGATED:
        print(f'       {path}  ({who})')
    print('       Removing an entry here does not fix a payload; it moves the finding back')
    print('       into the silent category. See catalog#345.')

# P1's own anchor: the call sites are known, so a drop in the count is a change in this
# matcher, not in the catalog. Without it, a regex regression reports "0 checked, ok".
#
# 9 is the number of INVOCATIONS across the whole catalog, counted by hand on 2026-11-18.
# The first version of this anchor said 13, because it counted every line containing
# `prpost` — `cp`, `test -x`, `chmod`, `command -v` and the text of a failure message all
# counted. An anchor that is wrong about the number it guards is the same defect class as
# the regex it is guarding.
EXPECTED_MIN = 9
if posted < EXPECTED_MIN:
    bad += 1
    print(f'  FAIL only {posted} call site(s) matched, expected at least {EXPECTED_MIN}.')
    print('       A dropped match reads as a passing check over fewer sites — that is how')
    print('       five sites went unchecked while this guard reported coverage.')

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
