#!/bin/sh
# dual_payload_test.sh — every comment this catalog posts must carry BOTH halves, and
# every post site must be visible to this check.
#
# WHY. Each message is two payloads in one comment: markdown for a human, and a fenced
# ```leartech-report block for an agent. That is the contract that lets a reader see the
# verdict and a machine act on it, and it is why `prpost` exists at all.
#
# It is also easy to break in a way nothing notices: a `prpost` call missing `-outcome`
# still posts (defaulting to success), missing `-check` still posts (against the wrong
# context), missing `-body-file` still posts (with only the summary as the body). None of
# those fail the step. The comment appears; it is just quietly wrong.
#
# THE CHECK IS NOW GO (go/tasklint payload). It was a python heredoc, and the `test`
# presubmit runs in golang:1.27.4 — no python3, no pyyaml — so it printed
# "SKIP: no pyyaml in this image" and exited 0 in the one place it exists to run. The
# rules (P1 call sites seen, P2 required flags, P3 the alias table, P4 one tool per task)
# and their tests live in go/tasklint; this file points it at the tree.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" payload -delegates "$ROOT/test/go/delegated_posters.txt" "$@"
