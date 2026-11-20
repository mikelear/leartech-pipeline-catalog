#!/bin/sh
# preview_failure_diag_test.sh — a preview timeout should say WHY, not just THAT.
#
# catalog#121, reported 2026-09-15 and still open: `end2end-ui` and `seo-preview`
# failed with three lines of log and nothing else, because the script runs
# `set -eo pipefail` with no ERR trap, so the log simply stops at the last
# successful echo. The outage was ours (a deleted DNS record) but the COST was
# diagnostic: it looked like a flake, and the only clue was that every `az/*`
# context passed the same commit.
#
# The same shape cost real time on 2026-11-20: a nine-day-dead `pr` step
# (catalog#334) surfaced as `FAIL: preview never reached stable ready state`
# after a ten-minute wait, naming neither an HTTP code nor whether the workload
# existed.
#
# So the timeout branch now samples three facts AT the failure and puts them in
# the PR comment:
#
#   the HTTP code        an answer means the preview exists and this PATH is wrong
#   the resolved address absent means it did not come up (DNS/Service/Ingress)
#   the deployments      `0/N` names the workload that is not starting
#
# This test fails if any of those disappears, because a diagnostic that is
# deleted is indistinguishable from one that did not exist — the log just stops
# sooner, which is the original defect.

set -u
cd "$(dirname "$0")/../.." || exit 1

fail=0
note() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

echo "=== preview failure diagnostics ==="

for f in tasks/end2end/pullrequest.yaml tasks/end2end-ui/pullrequest.yaml; do
  if [ ! -f "$f" ]; then
    note "$f is missing"
    continue
  fi

  # 1. the timeout branch samples the HTTP code at failure, without -f so a 404
  #    is REPORTED rather than treated as a transport error.
  if ! grep -q 'w .%{http_code}. -m 5' "$f"; then
    note "$f: the timeout branch stopped sampling the HTTP code at failure"
  fi

  # 2. ...and the resolved address.
  if ! grep -q 'getent hosts' "$f"; then
    note "$f: the timeout branch stopped resolving the host at failure"
  fi

  # 3. ...and a deployment's ready/desired, with the workload named.
  if ! grep -q 'GATE_NAME=' "$f"; then
    note "$f: the gate deployment is no longer named, so its state has no source"
  fi
  if ! grep -q 'readyReplicas' "$f"; then
    note "$f: the timeout branch stopped reading a deployment's readyReplicas"
  fi

  # 4. the facts reach the comment: written to the failures file this task's
  #    renderer consumes (e2end-why for the web lane, ui-why for the UI lane),
  #    and handed to that renderer.
  if ! grep -q 'At failure:' "$f"; then
    note "$f: the failure text stopped carrying an 'At failure:' line"
  fi
  if ! grep -Eq '(> /tmp/(e2end|ui)-why)' "$f"; then
    note "$f: the timeout branch stopped writing a failures file"
  fi
  if ! grep -Eq '(post_report|UI_RENDER).*(/tmp/(e2end|ui)-why)' "$f"; then
    note "$f: the failures file is not handed to the renderer"
  fi

  # 5. the sample happens AT the timeout. A sample taken before the wait
  #    reports the state that already failed to become ready, which is the
  #    same non-answer in more words.
  if ! awk '
      { line[NR] = $0 }
      /At failure:/ { at = NR }
      END {
        if (!at) exit 1
        for (i = at - 30; i <= at; i++) if (i > 0 && line[i] ~ /-ge "\$DEADLINE"/) exit 0
        exit 1
      }' "$f"; then
    note "$f: 'At failure:' is not inside the deadline branch (sampled too early)"
  fi
done

if [ "$fail" -eq 0 ]; then
  echo "  ok: both lanes sample HTTP code, resolution and deployment state at the timeout"
fi
exit "$fail"
