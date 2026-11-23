#!/bin/sh
# tool_callers_test.sh — a step that calls a CATALOG binary must run where that binary is.
#
# WHY. catalog#443: `tasks/chart-standard`'s render step ran in `jx-boot` — helm yes, chartstd
# no — and called `chartstd -list-toggles`. On any repo that DECLARED toggles the step died at
# "chartstd: command not found", and the FAIL line beside it named the toggles FILE, so a task
# defect read as a repo defect. A repo declaring none passed, because of an `if [ -f "$decl" ]`
# guard. The behavioural half of the chart gate had never run anywhere.
#
# Nothing caught it, and the guards that should have are the reason this exists:
#
#   chartstd_runs (renders)  asserts a rendering task MENTIONS chartstd — a mention says nothing
#                            about which image the step runs in, which is exactly the gap.
#   phase_tools (phases)     owns "the phase runs in an image with its tools", with a population
#                            of two hand-named files: tasks/end2end and tasks/end2end-ui.
#   the replay corpus        stubs the tool, so the image is never consulted.
#
# THE RULE, narrow enough to be knowable from this repo: a CATALOG binary is one the checkers
# image builds. This repo knows that image's contents and cannot know anybody else's (jx-boot,
# golang, node). So a step either runs in the checkers image, or the binary is STAGED from a
# checkers step in the same task (the provide-catalog-tools pattern), or the step BUILDS it
# itself, or its line goes in test/go/tool_callers.txt with the reason.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" tools -baseline "$ROOT/test/go/tool_callers.txt" "$@"
