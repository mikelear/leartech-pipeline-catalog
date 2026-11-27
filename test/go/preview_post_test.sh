#!/bin/sh
# preview_post_test.sh — the preview step posts its URL through prpost, and its exit code survives
# the pipe it uses to keep its own log.
#
# jx preview posted a comment (that is where "could not detect a preview URL" reached the PR from).
# When sppreview replaced it the step deployed the preview and told nobody, which is why the URL
# existed only in a step log. The catalog has ONE poster — go/prpost, via leartech-forge-api, which
# speaks both forges — so the step calls that rather than growing a curl block.
#
# The pipe is the subtle half: `sppreview create … | tee` reports TEE's exit status unless pipefail
# is on, so a failed deploy would have posted "Preview up" and exited 0. A green check that deployed
# nothing is the exact failure mode build_rc_guard_test.sh exists for, one layer along.

set -u
cd "$(dirname "$0")/../.." || exit 1

TASK=tasks/go/pullrequest.yaml
fail=0
note() { printf '  FAIL %s\n' "$1"; fail=$((fail + 1)); }

echo "=== the preview step posts its URL through prpost ==="

if [ ! -f "$TASK" ]; then
  note "$TASK is missing"
  exit 1
fi

# The preview step's script, extracted so a match cannot land in a different step.
step=$(awk '
  /^        - image: .*leartech-checkers/ { inimage = 1; buf = $0 "\n"; next }
  inimage && /^          name: preview$/ { isPreview = 1 }
  inimage && /^  podTemplate:/ { inimage = 0 }
  inimage { buf = buf $0 "\n" }
  END { printf "%s", buf }
' "$TASK")

if ! printf '%s' "$step" | grep -q 'name: preview'; then
  note "could not find the preview step in $TASK"
  exit 1
fi

POSTERS=$(printf '%s' "$step" | grep -c 'prpost -tool pr -check pr')
if [ "$POSTERS" -lt 1 ]; then
  note "the preview step posts nothing: expected a \`prpost -tool preview\` call"
fi

for flag in '-check pr' '-outcome' '-summary' '-body-file'; do
  printf '%s' "$step" | grep -q -- "$flag" || note "the preview's prpost call is missing $flag"
done

# A hand-rolled curl to a forge API is the thing prpost replaced; it must not come back here.
if printf '%s' "$step" | grep -qE 'curl .*(api\.github\.com|/api/v1/repos|forge-api)'; then
  note "the preview step curls a forge API directly instead of using prpost"
fi

# prpost authenticates through leartech-forge-api with a client_credentials mint, so the step needs
# the API URL and the client it mints with. Without them prpost exits 2 and the comment is absent
# for a reason nobody sees.
for name in FORGE_API_URL LEARTECH_AUTH_SERVER_URL LEARTECH_AUTH_CLIENT_ID LEARTECH_AUTH_CLIENT_SECRET; do
  printf '%s' "$step" | grep -q "name: $name" || note "the preview step does not set $name, so prpost cannot post"
done
if ! printf '%s' "$step" | grep -q 'name: catalog-prpost-oauth'; then
  note "the preview step does not read catalog-prpost-oauth, the client every other poster uses"
fi

# The exit code of the deploy must survive both the tee and the post.
printf '%s' "$step" | grep -q 'set -eo pipefail' ||
  note "the preview step pipes create into tee without pipefail, so a failed deploy reads as success"
printf '%s' "$step" | grep -qE 'sppreview create .*tee .*\|\| rc=\$\?' ||
  note "the preview step does not capture create's exit code around the pipe"
printf '%s' "$step" | grep -q 'exit "\$rc"' ||
  note "the preview step does not exit on the deploy's code, so the check cannot go red"

if [ "$fail" -eq 0 ]; then
  echo "  ok: the preview step posts through prpost and keeps the deploy's verdict"
fi
exit "$fail"
