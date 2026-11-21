#!/bin/sh
# inlined_runner_test.sh — the end2end runner inlined into the task equals run.sh.
#
# Inlining is deliberate: fetching the runner over raw.githubusercontent would put every
# end2end run in the estate behind GitHub's availability. The cost of that choice is drift,
# and this is what pays it — otherwise the pipeline executes a runner that passes none of
# run_test.sh.
#
# Structure is checked as well as content: every line of a YAML literal block must carry the
# block's indentation, INCLUDING the line the closing heredoc marker sits on. Getting that
# wrong emits a marker at column 0, ends the scalar early and leaves the task unparseable —
# which a content-only comparison reports as identical.
#
# WHERE IT RUNS NOW, and why it moved. This check used to live in end2end-runner/ and run in
# the lint step (debian:12-slim, apt-installing python3 to read the file). It is now
# go/tasklint inlined, so it runs here — in the `test` presubmit's golang image, beside the
# other tasklint subcommands, with no interpreter to install. `test/go/*_test.sh` is the
# glob the presubmit runs, so this file needed no wiring change to take effect.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" inlined
