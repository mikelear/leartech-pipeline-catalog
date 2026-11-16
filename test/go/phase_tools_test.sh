#!/bin/sh
# phase_tools_test.sh — every end2end phase must run in an image that HAS the tools the
# phase uses.
#
# THE FAILURE THIS EXISTS FOR. The end2end task was split into five named phases (catalog
# #215) so a failure would name its stage. Only the FIRST phase installed curl and
# kubectl — and a Tekton task's steps are separate containers that share /workspace and
# nothing else. So the wait phase ran in bare debian:12-slim, could not resolve the
# preview's Service, could not confirm the commit, fell back to the public URL, waited
# ten minutes and reported "preview never reached stable ready state" for a preview that
# may have been perfectly healthy.
#
# Every line of that is a consequence of a missing binary, and the report reads as a
# product failure. Two open PRs were red on it before anyone suspected the harness.
#
# So: parse the tasks, and for every phase assert
#   - it runs in the catalog's own image (tools pinned with the tag, not installed),
#     or in playwright-runner, which is the one image that must differ (browsers);
#   - no phase apt-gets or downloads a tool at run time — that is the bug it replaced;
#   - a phase that waits on a preview asserts curl and kubectl rather than assuming them.

set -u
cd "$(dirname "$0")/../.." || exit 1

echo "=== end2end phases run somewhere with their tools ==="

python3 - <<'PY' || exit 1
import sys

try:
    import yaml
except ImportError:
    print('SKIP: no pyyaml in this image')
    sys.exit(0)

fail = 0
for f in ('tasks/end2end/pullrequest.yaml', 'tasks/end2end-ui/pullrequest.yaml'):
    doc = yaml.safe_load(open(f))
    for t in doc['spec']['pipelineSpec']['tasks']:
        steps = t['taskSpec']['steps']
        phases = [s for s in steps if str(s.get('name', '')).startswith('end2end')]
        if not phases:
            continue
        images = set()
        for s in phases:
            name = s.get('name')
            img = str(s.get('image') or '')
            script = s.get('script') or ''
            images.add(img)

            if 'leartech-checkers' not in img and 'playwright-runner' not in img:
                print(f'  FAIL {f} :: {name} runs in {img!r}, which carries no guarantee of '
                      f'curl or kubectl')
                fail += 1

            for bad in ('apt-get install', 'apk add', 'dl.k8s.io'):
                if bad in script:
                    print(f'  FAIL {f} :: {name} does {bad!r} at run time; the image should '
                          f'carry the tool so the next phase has it too')
                    fail += 1

            if name in ('end2end-await', 'end2end-env', 'end2end-ui'):
                for tool in ('curl', ):
                    if tool not in script:
                        print(f'  FAIL {f} :: {name} never mentions {tool}, but it waits on '
                              f'a preview')
                        fail += 1
            if name == 'end2end-await' and 'kubectl' not in script:
                print(f'  FAIL {f} :: {name} never mentions kubectl, so it cannot resolve '
                      f'the preview\'s Service or confirm the commit')
                fail += 1
            if name == 'end2end-env' and 'command -v' not in script:
                print(f'  FAIL {f} :: {name} does not ASSERT its tools; a missing one then '
                      f'shows up three steps later as a broken preview')
                fail += 1

        if len(images) > 1 and not any('playwright-runner' in i for i in images):
            print(f'  FAIL {f}: the phases run in {len(images)} different images '
                  f'({sorted(images)}); one runtime means one set of tools')
            fail += 1
        else:
            print(f'  ok   {f}: {len(phases)} phase(s), all in '
                  f'{", ".join(sorted(images))}')

if fail:
    print()
    print('A phase whose container lacks its tools fails plausibly: it reports the')
    print('preview as broken. Assert the tool, and run where it is guaranteed.')
    raise SystemExit(1)
print('  ok   every phase has its tools')
PY

echo "=== ok ==="
