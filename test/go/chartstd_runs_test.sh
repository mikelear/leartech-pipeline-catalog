#!/bin/sh
# chartstd_runs_test.sh — every task that renders a chart also JUDGES it.
#
# WHY. `helm template` proves a chart parses. `chartstd` proves it says what the standard
# requires (S1–S12, T1/T2). A task that renders and does not judge is a green check over
# half the question — and that is exactly how the umbrellas passed for weeks while shipping
# tenant identity to every customer: `tasks/helm` rendered, and nothing read the render.
#
# THE CHECK IS NOW GO (go/tasklint renders). It was a python heredoc, and the `test`
# presubmit runs in golang:1.27.4 — no python3, no pyyaml — so it printed
# "SKIP: no pyyaml in this image" and exited 0 in the one place it exists to run.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" renders -baseline "$ROOT/test/go/chartstd_runs_baseline.txt" "$@"
