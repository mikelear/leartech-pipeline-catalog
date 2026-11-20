#!/bin/sh
# message_layer_test.sh — a ratchet on verdict-and-message logic written in shell.
#
# WHY. Everything an operator or an agent reads comes from a Tekton step's shell: the
# status context, the state, the markdown body, the machine block, the summary line.
# Measured in this catalog today: 62 markdown lines and 345 interpolated echo/printf
# lines compose that prose, and not one of them has a test.
#
# The Go tools that CARRY it do have tests — prpost (6), e2erun, e2eskipcontract, e2e
# (60+). What has no test is the layer that decides what to say. Every misreported
# verdict found in the 2026-11-16 session lived exactly there:
#
#   "CROSS-SERVICE AUDIENCE BINDING BROKEN"   a shell echo, about correct code
#   "Delta vs main: unavailable"              a shell branch, permanent for a whole forge
#   "0.0% (threshold 60%)"                    shell arithmetic on an empty profile
#   "preview never reached stable ready state" a shell wait, with no curl in the container
#   "2/5 checks passed, 16 skipped"           shell summary, read as "not applicable"
#
# So this does not demand a rewrite today. It demands that the number go DOWN and never
# up: each posting step moves its verdict and its prose into a Go reporter with a golden
# test, the baseline shrinks in the same PR, and the ratchet keeps it shrinking.
#
# A ratchet is the honest instrument while the work is in flight. A test that simply failed
# would red the catalog on day one and be disabled by the end of the week; one that allows
# drift would never be noticed at all.

set -u
cd "$(dirname "$0")/../.." || exit 1

BASELINE=test/go/message_layer_baseline.txt

echo "=== verdict-and-message logic written in shell (ratchet) ==="

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
    baseline = {}
    for line in open(baseline_path):
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        md, echos, path = line.split(None, 2)
        baseline[path] = (int(md), int(echos))
except FileNotFoundError:
    baseline = {}

# Two measures, both about a shell step deciding what to say:
#   md   — a line whose whole content is prose, a table row, an emoji verdict or the
#          "_Posted by_" footer. That is the body of a comment.
#   echo — an echo/printf that interpolates something. That is how a value becomes a
#          verdict in a sentence.
markdown = re.compile(r'^"?(###|:x:|:white_check_mark:|:warning:|:fast_forward:|:information_source:|:large_blue_circle:|\| |\*\*)')

# `| sed -n 's/.../.../' | head -1` is a PIPELINE, not a table row, and the table pattern above
# matches it. A table row's first cell is a word; a pipeline's is a binary. Same discriminator as
# the curl rule needed — a rule that counts shell plumbing as prose pushes people to keep prose in
# shell, which inverts the ratchet.
SHELL_AFTER_PIPE = re.compile(
    r'^\|\s*(sed|grep|awk|jq|head|tail|sort|cut|tr|wc|curl|cat|xargs|tee|uniq|paste|base64)\b')

def is_markdown(line):
    if SHELL_AFTER_PIPE.match(line):
        return False
    # A table ROW has at least two pipes: a leading one and the one that ends its first
    # cell. A shell pipeline continuation has exactly one, at the start --
    # tasks/gitops-cluster/pullrequest.yaml wraps its helm download as
    # `| tar -xz -C /tmp && mv ...`, and `tar` was not on the binary list above, so it
    # counted as a markdown row. A list of binaries can only ever be incomplete; the pipe
    # count is the property that actually distinguishes the two.
    if line.startswith('|') and line.count('|') < 2:
        return False
    return bool(markdown.match(line)) or '"_Posted by' in line

# NOT every interpolated printf is prose. `printf 'coverage=%s\n' "$TOTAL"` pipes a VALUE; it
# says nothing to a reader. Counting it would push a step to keep its formatting in shell to
# avoid the number going up, which inverts the point of the ratchet — the same false-positive
# lesson as the curl rule firing on "curl failed".
# After stripping the conversions and separators, a bare key name is data: `coverage=%s`
# says a value to a file, `Coverage: %s%%` says something to a person.
DATA_ONLY = re.compile(r'^[a-z_]+$')

def is_prose(line):
    """True when an echo/printf's own text would be read by a person."""
    m = re.match(r'^(?:echo|printf)\s+([\'"])(.*?)\1', line)
    if not m:
        # printf with an unquoted or variable format: assume prose, since we cannot tell.
        return True
    fmt = m.group(2)
    stripped = re.sub(r'%[-#0-9.]*[a-zA-Z]|\\n|\\t|\\\\', '', fmt)
    stripped = stripped.strip(' \'"=')
    return not DATA_ONLY.match(stripped)
posted = '"_Posted by'

current = {}
for f in sorted(glob.glob('tasks/**/*.yaml', recursive=True)):
    doc = yaml.safe_load(open(f))
    md = echos = 0
    for t in (doc.get('spec') or {}).get('pipelineSpec', {}).get('tasks') or []:
        for s in (t.get('taskSpec') or {}).get('steps') or []:
            script = s.get('script') or ''
            for line in script.splitlines():
                t2 = line.strip()
                if is_markdown(t2):
                    md += 1
                if re.match(r'^(echo|printf)\b', t2) and ('$' in t2 or '"' in t2) and is_prose(t2):
                    echos += 1
    if md or echos:
        current[f] = (md, echos)

total_md = sum(v[0] for v in current.values())
total_echo = sum(v[1] for v in current.values())
print(f'  {len(current)} task(s) compose a verdict or a message in shell')
print(f'  {total_md} markdown line(s), {total_echo} interpolated echo/printf line(s)')
print()

grew = []
for f, (md, echos) in sorted(current.items()):
    if f not in baseline:
        grew.append((f, f'new: {md} markdown, {echos} echo — not in the baseline'))
        continue
    b_md, b_echo = baseline[f]
    if md > b_md or echos > b_echo:
        grew.append((f, f'grew from {b_md}/{b_echo} to {md}/{echos} (markdown/echo)'))

if grew:
    print('  FAIL the message layer grew:')
    for f, why in grew:
        print(f'       {f}: {why}')
    print()
    print('A verdict or a sentence composed in shell has no test, because shell has')
    print('nowhere to put one. Move it into a Go reporter and lower its line in')
    print(f'{baseline_path} in the SAME PR — that is what makes this a ratchet.')
    raise SystemExit(1)

shrank = []
for f, (b_md, b_echo) in baseline.items():
    md, echos = current.get(f, (0, 0))
    if md < b_md or echos < b_echo:
        shrank.append((f, b_md, b_echo, md, echos))

if shrank:
    print('  Note: these moved the right way — update the baseline so it stays down:')
    for f, b_md, b_echo, md, echos in shrank:
        print(f'       {f}: {b_md}/{b_echo} -> {md}/{echos}')
    print()

print('  ok   no task added shell-composed verdicts or prose')
PY

echo "=== ok ==="
