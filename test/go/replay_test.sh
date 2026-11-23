#!/bin/sh
# replay_test.sh — recorded PRs, replayed against the shipped tasks.
#
# WHY. Three times in one session the answer to "does this work?" came from launching the real
# thing by hand — restarting a pod to prove the GCS key was gone, running scopedrift against
# real checkouts, launching kyverno as a pod. Each was a defect that HAD shipped. The suite
# could say a tool works on its fixtures; nothing could say "this task, on this PR, reaches
# this verdict".
#
# Each fixture under test/replay/ is a directory:
#
#   task          the task to run, repo-relative (tasks/go/pullrequest.yaml)
#   workspace/    copied to /workspace/source — the PR's checkout
#   shared/       copied to /workspace — the rc files and logs steps hand to each other
#   env           KEY=VALUE lines, Tekton's env, handed to every step
#   expect.yaml   why it exists, which steps to replay (only:), and the verdict each reaches
#
# The steps run against stub binaries on PATH, exactly as the end2end phase harness runs the
# phase scripts, so a replay costs seconds and needs no cluster, forge or registry.
#
# The first two fixtures are catalog#334: a failing go build must stop the image build and the
# preview. On the day that guard was written it fired on EVERY run (it tested whether the rc
# FILE existed, and it always does), so nine days of Go PRs shipped no image and no preview and
# every PR was green. Replaying it then would have failed immediately.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" replay -fixtures "$ROOT/test/replay" "$@"
