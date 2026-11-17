#!/bin/bash
# phases_test.sh — run the end2end task's steps, in order, against fixtures.
#
# WHY. The end2end gate used to be one 860-line script; it is now five named
# steps (end2end-env, -contract, -await, -run, -report) that share the pod's
# /workspace but NOT its environment, passing state through files that each
# producer rewrites. That plumbing has its own failure modes and they are all
# silent: a step that cannot see HAS_SCRIPT runs the suite on a repo that
# declared none, and a step that cannot see SCRIPT_EXIT reports the wrong
# verdict. Neither shows up in the pipeline as anything but a red or a green.
#
# So the steps are executed here, out of the parsed YAML, against a fake
# workspace — the same text Tekton would run, not a copy of it.
#
# The matrix this pins:
#   declared end2end/NONE      -> contract exits 0, the later phases no-op
#   neither run.sh nor NONE    -> contract exits 1
#   suite passes               -> report exits 0
#   suite fails                -> report exits 1
#   suite never writes results -> run exits 0 (the verdict is the report's),
#                                 and the report names end2end-run in its log
#
# Needs bash and python3. No YAML library: this runs in the lint step's image,
# which has jq and python3 but no pyyaml, and a test that cannot start is not a
# test.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

TASK=../tasks/end2end/pullrequest.yaml
fail=0
ok()  { printf '  ok   %s\n' "$1"; }
bad() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ---- the steps, exactly as the task declares them ------------------------
mkdir -p "$WORK/steps" "$WORK/ws/bin" "$WORK/ws/source" "$WORK/src/.jx" "$WORK/bin"
python3 - "$TASK" "$WORK/steps" "$WORK/ws" <<'PY' || exit 1
import re, sys

task, outdir, ws = sys.argv[1], sys.argv[2], sys.argv[3]
want = ['end2end-env', 'end2end-contract', 'end2end-await', 'end2end-run', 'end2end-report']
found, step, buf = {}, None, None

def flush():
    if step and buf is not None:
        found[step] = ''.join(l + '\n' for l in buf)

for line in open(task).read().splitlines():
    if re.match(r'^        - ', line):
        flush()
        step, buf = None, None
        continue
    m = re.match(r'^          name: (\S+)$', line)
    if m and step is None:
        step = m.group(1)
        continue
    if re.match(r'^          script: \|$', line):
        buf = []
        continue
    if buf is not None:
        if line.startswith('            '):
            buf.append(line[12:])
        elif line.strip() == '':
            buf.append('')
        else:
            flush()
            buf = None
flush()

missing = [w for w in want if w not in found]
if missing:
    print(f"FAIL: the task no longer declares {missing}; update this test with the split")
    raise SystemExit(1)
for name in want:
    open(f"{outdir}/{name}.sh", 'w').write(found[name].replace('/workspace', ws))
PY

# ---- a fake pod: workspace, repo, and the tools the steps call ----------
cat > "$WORK/src/.jx/variables.sh" <<'EOF'
export APP_NAME=myapp DOMAIN=jx.leartech.com PULL_NUMBER=7 REPO_OWNER=acme REPO_NAME=thing VERSION=1
EOF
printf '#!/bin/sh\nexit 0\n' > "$WORK/bin/apt-get"
printf '#!/bin/sh\nexit 0\n' > "$WORK/bin/kubectl"
cat > "$WORK/bin/curl" <<'EOF'
#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in -o) shift; : > "$1";; esac; shift; done
exit 0
EOF
chmod +x "$WORK/bin/apt-get" "$WORK/bin/kubectl" "$WORK/bin/curl"
# The env step downloads kubectl to an absolute path; keep it in the sandbox.
sed "s|/usr/local/bin/kubectl|$WORK/bin/kubectl|g" "$WORK/steps/end2end-env.sh" > "$WORK/steps/env.sh"
mv "$WORK/steps/env.sh" "$WORK/steps/end2end-env.sh"

# e2erun, as the checkers image provides it: writes results.json, exits.
# A stub prpost. The report step now RENDERS its comment in Go; the rendering is pinned by
# go/prpost's golden scenarios, so what this harness checks is the plumbing — that the phase
# supplies the outcome and summary it should. A stub is the right seam: the alternative is a Go
# toolchain and the whole renderer inside a shell test about phase wiring.
# The harness cd's to its own directory at the top, so the stub is beside us as ./fake-prpost.sh.
cp ./fake-prpost.sh "$WORK/ws/bin/prpost"
chmod +x "$WORK/ws/bin/prpost"

fake_e2erun() {
  {
    printf '#!/bin/bash\n'
    printf 'cat > "$1/results.json" <<J\n'
    printf '%s\n' "$1"
    printf 'J\n'
    printf 'exit %s\n' "${2:-0}"
  } > "$WORK/ws/bin/e2erun"
  chmod +x "$WORK/ws/bin/e2erun"
}
PASS_JSON='{"success": true, "summary": "2/2 checks passed", "tests": [{"name":"a","status":"pass"},{"name":"b","status":"pass"}]}'
FAIL_JSON='{"success": false, "summary": "1 of 2 failed", "tests": [{"name":"a","status":"pass"},{"name":"b","status":"fail"}]}'

RC=0
OUT=""
run_step() {   # run_step <name> -> exit code in RC, output in OUT
  OUT=$( cd "$WORK/src" && PATH="$WORK/bin:$PATH" bash "$WORK/steps/$1.sh" 2>&1 )
  RC=$?
}

fresh() {
  rm -rf "$WORK/ws/.end2end" "$WORK/src/end2end"
  mkdir -p "$WORK/src/end2end"
}

echo "=== end2end task phases ==="

# 1. declared opt-out: the whole chain stays green
fresh
printf 'no suite: this service is a chart library\n' > "$WORK/src/end2end/NONE"
run_step end2end-env;      [ "$RC" = 0 ] && ok "env: exits 0" || { bad "env: exits 0 (got $RC)"; echo "$OUT" | tail -3; }
[ -f "$WORK/ws/.end2end/env" ] && ok "env: publishes the preview variables" || bad "env: publishes the preview variables"
[ -f "$WORK/ws/.end2end/lib.sh" ] && ok "env: publishes the shared functions" || bad "env: publishes the shared functions"
run_step end2end-contract; [ "$RC" = 0 ] && ok "contract: declared NONE exits 0" || { bad "contract: declared NONE exits 0 (got $RC)"; echo "$OUT" | tail -3; }
echo "$OUT" | grep -q 'SKIPPED: no end2end suite, declared' && ok "contract: says nothing was tested" || bad "contract: says nothing was tested"
for s in end2end-await end2end-run end2end-report; do
  run_step "$s"
  if [ "$RC" = 0 ] && echo "$OUT" | grep -q 'this phase is a no-op'; then
    ok "$s: no-ops on a declared skip"
  else
    bad "$s: no-ops on a declared skip (rc=$RC)"
    echo "$OUT" | tail -3
  fi
done

# 2. neither run.sh nor NONE: the check fails, and names its branch
fresh
run_step end2end-contract
[ "$RC" = 1 ] && ok "contract: undeclared repo exits 1" || bad "contract: undeclared repo exits 1 (got $RC)"

# 3. a passing suite
fresh
printf '#!/bin/bash\necho ran\n' > "$WORK/src/end2end/run.sh"; chmod +x "$WORK/src/end2end/run.sh"
fake_e2erun "$PASS_JSON" 0
run_step end2end-env
run_step end2end-contract; [ "$RC" = 0 ] && ok "contract: declared suite exits 0" || { bad "contract: declared suite exits 0 (got $RC)"; echo "$OUT" | tail -3; }
grep -q 'HAS_SCRIPT="1"' "$WORK/ws/.end2end/env.contract" && ok "contract: publishes HAS_SCRIPT for the later phases" || bad "contract: publishes HAS_SCRIPT for the later phases"
run_step end2end-run;      [ "$RC" = 0 ] && ok "run: exits 0 on a passing suite" || { bad "run: exits 0 on a passing suite (got $RC)"; echo "$OUT" | tail -3; }
run_step end2end-report;   [ "$RC" = 0 ] && ok "report: exits 0 on a passing suite" || { bad "report: exits 0 on a passing suite (got $RC)"; echo "$OUT" | tail -3; }
echo "$OUT" | grep -q 'PASS: end2end complete' && ok "report: prints the verdict" || bad "report: prints the verdict"
# The comment is rendered in Go; what this harness checks is that the phase hands it the right
# OUTCOME. The stub echoes the outcome back, so a phase that always said "failure" would show.
if echo "$OUT" | grep -q 'outcome=success'; then ok "report: hands the renderer outcome=success"; else bad "report: hands the renderer outcome=success"; echo "$OUT" | tail -6; fi

# 4. a failing suite: the verdict lands in the report step, not the run step
fresh
printf '#!/bin/bash\necho ran\n' > "$WORK/src/end2end/run.sh"; chmod +x "$WORK/src/end2end/run.sh"
fake_e2erun "$FAIL_JSON" 0
run_step end2end-env; run_step end2end-contract; run_step end2end-run
[ "$RC" = 0 ] && ok "run: does not decide the verdict" || bad "run: does not decide the verdict (got $RC)"
run_step end2end-report
[ "$RC" = 1 ] && ok "report: exits 1 on a failing suite" || bad "report: exits 1 on a failing suite (got $RC)"
echo "$OUT" | grep -q 'outcome=failure' && ok "report: hands the renderer outcome=failure" || bad "report: hands the renderer outcome=failure"

# 5. a suite that dies without results.json: the report names the step to read
fresh
printf '#!/bin/bash\nexit 127\n' > "$WORK/src/end2end/run.sh"; chmod +x "$WORK/src/end2end/run.sh"
rm -f "$WORK/ws/bin/e2erun"
run_step end2end-env; run_step end2end-contract; run_step end2end-run
grep -q 'SCRIPT_EXIT="127"' "$WORK/ws/.end2end/env.run" && ok "run: publishes the suite's exit code" || bad "run: publishes the suite's exit code"
run_step end2end-report
[ "$RC" = 1 ] && ok "report: exits 1 with no results.json" || bad "report: exits 1 with no results.json (got $RC)"
if echo "$OUT" | grep -q 'end2end-run'; then
  ok "report: names the step whose logs explain it"
else
  bad "report: names the step whose logs explain it"
  echo "$OUT" | tail -6
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "FAIL: $fail assertion(s)."
  exit 1
fi
echo "=== ok ==="
