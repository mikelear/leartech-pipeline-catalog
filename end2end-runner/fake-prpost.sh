#!/bin/bash
# A stub prpost for the phase harness.
#
# A stub for the phase harness. Rendering is pinned by go/prpost's golden scenarios; what this
# harness checks is that the phase hands the renderer the right outcome, so the stub echoes the
# arguments it was given.
set -uo pipefail

outcome=""; summary=""; tool=""
while [ $# -gt 0 ]; do
  case "$1" in
    -outcome) outcome="${2:-}"; shift 2 ;;
    -summary) summary="${2:-}"; shift 2 ;;
    -tool)    tool="${2:-}"; shift 2 ;;
    -render)  render=1; shift ;;
    *) shift ;;
  esac
done

# Say what we were handed, on stderr: the harness asserts the phase passed the right outcome,
# and stderr keeps that out of the comment body it is checking.
printf 'fake-prpost: tool=%s outcome=%s summary=%s\n' "${tool:-}" "${outcome:-}" "${summary:-}" >&2

if [ "${render:-0}" = "1" ]; then
  printf '<!-- %s -->\n' "leartech-${tool:-unknown}"
  printf '**%s** — %s\n' "$outcome" "$summary"
  printf '```leartech-report\n{"tool":"%s","outcome":"%s"}\n```\n' "$tool" "$outcome"
  exit 0
fi
# A post: the harness has no forge, and post_sticky treats a failure as a warning.
exit 0
