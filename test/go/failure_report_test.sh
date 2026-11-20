#!/bin/sh
# failure_report_test.sh — a task that can fail non-zero and post nothing is a red check
# whose only message is Lighthouse's generic table.
#
# WHY. `console-api#31` was a failing check whose reason sat in a log nobody was told to
# open. Every task in the baselines below fails that way today, and the list may only
# SHRINK: fixing a task means giving it a report on its failure branch
# (tasks/go-lint/pullrequest.yaml is the worked example) and deleting its line in the same
# PR.
#
# THIS CHECK HAD NO CALLER UNTIL NOW. It was a python heredoc that parsed YAML with PyYAML,
# so it could not run in the `test` presubmit's golang image — and rather than skip, it was
# simply never wired in. It is now a go/tasklint subcommand, its `go test` runs in the
# presubmit's `go/*/go.mod` loop, and this file is wired into the same step as the rest.
#
# Two kinds of task it names, and they are different findings:
#
#   explicit  `exit 1` — a branch the author wrote. Ratcheted; a new one fails.
#   implicit  `set -e` and a command that fails — how MOST tasks fail. Reported as an
#             inventory, because failing on all of them at once would be a cliff.
#
# And one shape it refuses: a step named `report` with no `onError: continue` can never
# run, because Tekton abandons the remaining steps the moment one exits non-zero. That
# reads, from the file, exactly like a task that reports.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" \
  failures \
  -baseline "$ROOT/test/go/failure_report_baseline.txt" \
  -delegates "$ROOT/test/go/delegated_posters.txt" "$@"
