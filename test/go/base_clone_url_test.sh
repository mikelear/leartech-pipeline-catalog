#!/bin/sh
# base_clone_url_test.sh — assert the delta-coverage baseline clones from the
# forge the PR actually came from.
#
# The bug this pins: the mk built the baseline URL as
# https://github.com/<owner>/<name>.git unconditionally. On a Forgejo PR that URL
# is a different host, so the clone failed and every forge PR carried
# "Delta vs main: unavailable" — which reads as a transient, but silently turns
# the coverage ratchet OFF for that whole forge.
#
# Three things must hold, and the third is the one worth guarding: a remote that
# names a DIFFERENT repository must not be able to redirect the baseline, because
# then the delta would compare this PR against some other repo's coverage and
# pass on a regression.

set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
MK="$REPO_ROOT/go/leartech-go.mk"

fails=0
check() {
  desc=$1; want=$2; shift 2
  got=$(make -s -f "$MK" base-clone-url OWNER=it-test NAME=lh-test "$@")
  if [ "$got" = "$want" ]; then
    printf '  ok   %s\n' "$desc"
  else
    printf '  FAIL %s\n       want: %s\n       got:  %s\n' "$desc" "$want" "$got"
    fails=$((fails + 1))
  fi
}

echo "=== delta-coverage base clone URL ==="

check "forge remote -> the forge host" \
  "https://forgejo.jx.leartech.com/it-test/lh-test.git" \
  ORIGIN_URL=https://forgejo.jx.leartech.com/it-test/lh-test.git

check "forge remote with a port -> kept verbatim" \
  "https://forgejo.internal:8443/it-test/lh-test.git" \
  ORIGIN_URL=https://forgejo.internal:8443/it-test/lh-test.git

check "github remote -> github" \
  "https://github.com/it-test/lh-test.git" \
  ORIGIN_URL=https://github.com/it-test/lh-test.git

check "no remote -> github.com default" \
  "https://github.com/it-test/lh-test.git" \
  ORIGIN_URL=

check "remote naming another repo -> github.com default, not the other repo" \
  "https://github.com/it-test/lh-test.git" \
  ORIGIN_URL=https://github.com/mikelear/lighthouse.git

if [ "$fails" -ne 0 ]; then
  echo
  echo "FAIL: $fails case(s). The baseline clone must follow the PR's own forge."
  exit 1
fi

echo "=== ok ==="
