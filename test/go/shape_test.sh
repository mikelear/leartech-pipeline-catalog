#!/bin/sh
# shape_test.sh — every task file must have the shape TEKTON accepts, not merely valid YAML.
#
# WHY. `resources:resources:` shipped on main in tasks/helm/release.yaml. A text splice
# doubled the key; YAML accepts that as the field "resources:resources" with an empty value,
# so every YAML gate in the estate passed — and Tekton would have rejected the whole task the
# first time a helm umbrella repo's release pipeline ran it. Nothing in the catalog could see
# it, because the catalog never instantiates that task.
#
# Two rules, both about text a parser will happily accept:
#   X1  a mapping key may not contain ':' (a spliced key)
#   X2  a step may only carry fields Tekton defines for a step (a typo is a rejection)
#
# THE CHECK IS GO (go/tasklint shape), for the reason task_syntax_test.sh records: the `test`
# presubmit runs in golang:1.27.4, so a python/pyyaml heredoc would SKIP here — in the one
# place it exists to run. The logic and its tests live in go/tasklint, whose `go test` the
# presubmit already runs; this file only points it at the tree.
set -u
cd "$(dirname "$0")/../.." || exit 1

( cd go/tasklint && go run . -dir ../.. shape )
