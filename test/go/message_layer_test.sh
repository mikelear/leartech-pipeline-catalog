#!/bin/sh
# message_layer_test.sh — how much prose a task composes in shell, as a ratchet.
#
# A verdict or a sentence written in shell has no test, because shell has nowhere to put
# one. `catalog#271` was a comment that reported success on failures; `console-api#31` was
# a red check whose only message was Lighthouse's generic table. Both are the same defect:
# the thing that decided the verdict was a string in a step.
#
# THE MEASUREMENT IS NOW GO (go/tasklint message). This file was a python heredoc, and the
# `test` presubmit runs in golang:1.27.4 — no python3, no pyyaml — so it printed
# "SKIP: no pyyaml in this image" and exited 0 (measured on catalog#356's and #359's own
# pipeline runs). That skip cost us a real violation: `#348` added
# tasks/gitops-cluster with 1 markdown + 7 echo lines, this check fails on it, and it
# merged. The logic, its tests and the two measures now live in go/tasklint.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" message -baseline "$ROOT/test/go/message_layer_baseline.txt" "$@"
