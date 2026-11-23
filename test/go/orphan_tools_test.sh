#!/bin/sh
# orphan_tools_test.sh — a tool nothing runs is a note, not a check.
#
# WHY. Four gates in this catalog were built, tested, shipped in the checkers image, and never
# invoked: `argohooks` (catalog#429, wired in #431), `identitystd` (no caller until gsm#3236),
# `scopedrift` (same, and it reported 15 findings about two repos it could not read the first
# time it was pointed at real ones), and `versionanchor` (its logic is inlined as shell in
# tasks/end2end/pullrequest.yaml, so the tested Go tool beside it is a reference nobody runs).
#
# Every one of them passed its own tests the whole time. That is the failure this check
# records: the catalog's suite answered "does this tool work?" and never "does anything call
# this tool?" — so a green build said nothing about four gates being dead.
#
# THE POPULATION IS THE IMAGE'S BUILD LIST, read from checkers/Dockerfile, because a binary in
# the image is what a task can call. A tool has a caller when it is named as a COMMAND in a
# task, in go/leartech-go.mk (which every Go repo's `make lint` runs), or in .lighthouse/
# (this catalog's own presubmit); a test is not a caller. Anything else needs a line in
# test/go/orphan_tools.txt with the reason — that file may only shrink.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" orphan -baseline "$ROOT/test/go/orphan_tools.txt" "$@"
