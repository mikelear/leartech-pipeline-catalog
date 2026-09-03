#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
mk="$root/go/leartech-go.mk"
task="$root/tasks/go-lint/pullrequest.yaml"
readme="$root/README.md"

fail=0
report() { echo "FAIL: $1"; fail=1; }

mk_v=$(sed -n 's/^GOLANGCI_VERSION[[:space:]]*?=[[:space:]]*\([0-9][0-9.]*\).*/\1/p' "$mk" | head -1)
task_v=$(sed -n 's/.*golangci\/golangci-lint:v\([0-9][0-9.]*\).*/\1/p' "$task" | head -1)
readme_v=$(sed -n 's/.*`golangci-lint`[[:space:]]*|[[:space:]]*`\([0-9][0-9.]*\)`.*/\1/p' "$readme" | head -1)

[ -n "$mk_v" ]     || report "no GOLANGCI_VERSION found in go/leartech-go.mk"
[ -n "$task_v" ]   || report "no golangci/golangci-lint:vX image found in tasks/go-lint/pullrequest.yaml"
[ -n "$readme_v" ] || report "no golangci-lint row found in the README canonical version table"

echo "  go/leartech-go.mk GOLANGCI_VERSION   = ${mk_v:-<none>}"
echo "  tasks/go-lint step image             = ${task_v:-<none>}"
echo "  README canonical version table       = ${readme_v:-<none>}"

if [ -n "$mk_v" ] && [ -n "$task_v" ] && [ "$mk_v" != "$task_v" ]; then
  report "leartech-go.mk ($mk_v) and the go-lint step image ($task_v) disagree.
      CI would lint with $task_v while a developer running 'make lint' uses $mk_v,
      so a linter present in one and not the other passes locally and fails in CI."
fi

if [ -n "$mk_v" ] && [ -n "$readme_v" ] && [ "$mk_v" != "$readme_v" ]; then
  report "leartech-go.mk ($mk_v) and the README table ($readme_v) disagree.
      The README is where a developer reads which version to install locally."
fi

readme_img=$(sed -n 's/.*golangci\/golangci-lint:v\([0-9][0-9.]*\).*/\1/p' "$readme" | head -1)
if [ -n "$readme_img" ] && [ -n "$task_v" ] && [ "$readme_img" != "$task_v" ]; then
  report "the README quotes step image v$readme_img but the task pins v$task_v"
fi

if [ "$fail" -eq 0 ]; then
  echo "PASS: golangci-lint is pinned to $mk_v in all three places"
  exit 0
fi
exit 1
