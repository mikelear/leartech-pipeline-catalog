#!/bin/sh
# phase_tools_test.sh — every end2end phase runs in an image that carries its tools.
#
# WHY. A Tekton task's steps are separate containers that share only /workspace, so a tool
# installed in one step is not in the next. The end2end task used to apt-get curl and
# download kubectl in its FIRST step and then run four more in plain debian:12-slim —
# where neither exists. The wait phase therefore reported "kubectl or PULL_PULL_SHA
# unavailable" and probed a Service URL it had never resolved, and the failure read as a
# broken PREVIEW rather than a container with no curl.
#
# Each phase asserts:
#   - it runs in an image that guarantees the tools (checkers or playwright-runner);
#   - it does not install anything at run time (a tool fetched here is absent in the next
#     step, which is the whole bug);
#   - the phases that wait on a preview at least MENTION the tool they need;
#   - end2end-env ASSERTS its tools, so a missing one fails there rather than three steps
#     later as a plausible-looking preview failure.
#
# THE CHECK IS NOW GO (go/tasklint phases). It was a python heredoc, and the `test`
# presubmit runs in golang:1.27.4 — no python3, no pyyaml — so it printed
# "SKIP: no pyyaml in this image" and exited 0 in the one place it exists to run.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" phases "$@"
