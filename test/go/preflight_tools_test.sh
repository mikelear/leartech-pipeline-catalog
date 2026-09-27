#!/bin/sh
# preflight-tools is a DECLARATION three things read, so its shape is a
# contract rather than a convenience:
#
#   - an image build asserts every `path` tool is present, so the image and
#     the mk cannot drift;
#   - an unattended agent reconciles it against `command -v` and reports the
#     delta, so a red CI run is attributable to a missing tool rather than to
#     the agent skipping the step;
#   - a person runs it to see why a gate will not work where they are.
#
# A malformed line breaks all three silently, which is why the shape is
# tested rather than trusted.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
mk="$root/go/leartech-go.mk"

fail=0
report() { echo "FAIL: $1"; fail=1; }

out=$(make -f "$mk" preflight-tools 2>/dev/null) || {
  echo "FAIL: 'make -f go/leartech-go.mk preflight-tools' did not run"
  exit 1
}

[ -n "$out" ] || report "preflight-tools printed nothing; the declaration is empty"

# THREE TAB-SEPARATED COLUMNS ON EVERY LINE. A consumer splitting on tabs
# gets a silently short record otherwise, and the `via` column — the one
# that says whether a tool works in a sandboxed pod — is the one it loses.
lines=0
printf '%s\n' "$out" | while IFS='	' read -r tool via gates rest; do
  lines=$((lines + 1))
  [ -n "$tool" ]  || report "line $lines: empty tool name"
  [ -n "$via" ]   || report "line $lines ($tool): empty via — a consumer cannot tell whether this works without docker or egress"
  [ -n "$gates" ] || report "line $lines ($tool): empty gates — nothing says what stops working without it"
  [ -z "$rest" ]  || report "line $lines ($tool): more than three columns ($rest)"

  case "$via" in
    path|path-or-docker|docker|curl|go-install) ;;
    *) report "line $lines ($tool): via=$via is not one of path|path-or-docker|docker|curl|go-install" ;;
  esac
done

# THE TOOLS THE GATES ACTUALLY SHELL OUT TO MUST BE DECLARED. This is the
# half that rots: a new gate reaches for a binary and nobody adds the row,
# so an image passes its composition test while lacking the tool.
for t in go golangci-lint govulncheck yq hadolint; do
  printf '%s\n' "$out" | cut -f1 | grep -qx "$t" \
    || report "$t is used by a gate in this file but is not declared by preflight-tools"
done

# EVERY DECLARED `path` TOOL IS REACHED BY NAME SOMEWHERE IN THE FILE, so a
# stale row is caught as well as a missing one. A declaration nothing uses
# sends an image build off baking something no gate needs.
printf '%s\n' "$out" | while IFS='	' read -r tool via _rest; do
  case "$via" in
    path|path-or-docker)
      grep -q "$tool" "$mk" \
        || report "$tool is declared but appears nowhere in the mk; a stale row makes an image bake what no gate uses"
      ;;
  esac
done

# THE PIN IS THE CHECK, AND A LOCAL BINARY IS NOT IT. dockerfile-lint must
# prefer the pinned image: hadolint 2.14.0 reports nothing on a file
# v2.15.1 flags DL3066 on, so an unpinned binary taking precedence would
# make local and CI disagree by default.
docker_line=$(grep -n 'runner="docker"' "$mk" | head -1 | cut -d: -f1)
binary_line=$(grep -n 'runner="binary"' "$mk" | head -1 | cut -d: -f1)
if [ -n "$docker_line" ] && [ -n "$binary_line" ]; then
  [ "$docker_line" -lt "$binary_line" ] \
    || report "dockerfile-lint prefers a local hadolint over the pinned image; the pin exists because a version change moves the findings"
else
  report "dockerfile-lint no longer chooses between a pinned image and a local binary"
fi

echo "  preflight-tools declares $(printf '%s\n' "$out" | wc -l | tr -d ' ') tool(s)"
[ "$fail" = 0 ] && echo "PASS: preflight-tools" || exit 1
