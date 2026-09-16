#!/bin/sh
# version_stamp_test.sh — every task that kaniko-builds a Go image must pass
# --build-arg VERSION, so the binary's ldflags stamp is real.
#
# This is a PARITY guard between the go task variants, not a rule change.
#
# Why it exists: go-platform-promote built with kaniko and used $VERSION for
# the image TAG on one line while omitting --build-arg VERSION on the line
# above. Every service on that variant therefore shipped a binary whose
# ldflags stamp was still the Dockerfile default, and leartech-ai-gateway's
# GET /version reported {"version":"dev"} on both clusters for real releases.
#
# The tag was right and the binary was wrong, so nothing looked broken:
# `kubectl get deploy` showed 0.0.60, the image said 0.0.60, and only the
# running process disagreed. Two of the three variants passed it and one did
# not, which is exactly the drift a parity test catches and a reader does not.
#
# Exits non-zero when a variant kaniko-builds without stamping.
set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)

echo "==> version_stamp_test.sh: every kaniko Go build passes --build-arg VERSION"

checked=0
failed=0
for f in "$REPO_ROOT"/tasks/go*/release.yaml; do
  [ -f "$f" ] || continue
  # Only tasks that actually build an image are in scope.
  grep -q 'kaniko/executor' "$f" || continue
  checked=$((checked + 1))
  name=$(echo "$f" | sed "s|$REPO_ROOT/||")
  if grep -q 'build-arg VERSION' "$f"; then
    echo "  ok    $name"
  else
    echo "  FAIL  $name kaniko-builds without --build-arg VERSION"
    echo "        the image tag will be right and the binary's ldflags stamp"
    echo "        will be the Dockerfile default, so /version lies"
    failed=$((failed + 1))
  fi
done

if [ "$checked" -eq 0 ]; then
  echo "FAIL: examined 0 tasks. A pass here would mean the opposite of what it says."
  exit 1
fi

if [ "$failed" -gt 0 ]; then
  echo "FAIL: $failed of $checked kaniko Go builds do not stamp VERSION"
  exit 1
fi

echo "==> version_stamp_test.sh: $checked task(s) stamp VERSION"
