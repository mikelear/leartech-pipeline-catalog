#!/bin/sh
# env_refs_test.sh — a step's fatal ${VAR:?...} must be declared in its env.
#
# WHY. `${VAR:?}` exits the shell on the spot when VAR is unset or empty. That is the right
# behaviour for a step configured wrong — except when the step never had VAR at all, because
# then it dies at the very moment it was supposed to do its job.
#
# MEASURED, leartech-console-api#37: `make lint` failed at comment-gate (2 prose comment lines,
# 0 test lines). The report step that exists to say so died at
#
#   /tekton/scripts/script-4-sxk9x: line 10: CLUSTER_ID: parameter null or not set
#
# and posted NOTHING. The PR showed only Lighthouse's generic "the following test failed" — which
# is console-api#31, in the step whose whole purpose is to prevent it. The pod spec confirmed
# step-report had no CLUSTER_ID env at all; jx-boot-job-env-vars carries only DEV_ and
# JX_REPOSITORY_ credentials.
#
# Three tasks shipped it: go-lint, chart-standard and values-standard. Every sibling declares
# CLUSTER_ID from jx-cluster-config, so the pattern was right everywhere it was copied from — and
# the three that forgot were invisible on a passing PR, because a report step only runs on failure.
#
# THE RULE: every ${VAR:?...} must appear in that step's `env:` or the taskSpec's
# `stepTemplate.env:`. `envFrom` does not count — an `optional: true` secret reference is a hope,
# not a declaration, and treating it as one is what made the three offenders look configured.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT/go/tasklint" || exit 1
exec go run . -dir "$ROOT" envrefs "$@"
