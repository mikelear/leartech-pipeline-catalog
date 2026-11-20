#!/bin/sh
# task_syntax_test.sh — every step script in the catalog must PARSE.
#
# WHY. Three task steps have never been valid shell: the
# `if [ -n "$GIT_TOKEN" ] && [ -n "$PULL_NUMBER" ]; then` block that posts the sticky
# comment was opened and never closed in dotnet-test, rust-test and dotnet-mutants.
# A parse error kills the step before it runs a line, so the visible symptom is
# "no coverage comment" or a bare failed step — never "the script is broken".
#
# Then two broke in a different way on 2026-11-16: moving the poster out of a
# `( cd /tmp/pp && go run … )` subshell left the closing paren behind, in four more
# files. Same class: a script that cannot start, reporting as a gate that failed.
#
# `bash -n` is the cheapest check there is, and it catches both. It does not execute
# anything, needs no cluster, and takes about a second for the whole catalog.
#
# THE CHECK IS NOW GO (go/tasklint). It used to be a python heredoc, and the `test`
# presubmit runs in golang:1.27.4 — no python3, no pyyaml — so it printed
# "SKIP: no pyyaml in this image" and exited 0 in the one place it exists to run
# (measured on catalog#359's own pipeline run). The logic and its tests live in
# go/tasklint, whose `go test` the presubmit already runs; this file only points it at
# the tree, so there is one implementation and no shell to drift.
set -u
cd "$(dirname "$0")/../.." || exit 1

( cd go/tasklint && go run . -dir ../.. syntax )
