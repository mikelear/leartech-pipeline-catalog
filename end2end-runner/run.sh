#!/usr/bin/env bash
# THE end2end runner. One copy, for every repo.
#
# This script existed in 14 repos as five different programs — the same runner
# at five different ages. Measured 2026-09-13, hashing executable lines only:
#
#   10bec0ea  4 repos   ai-gateway, artifact-api, go-service-template, mcp-servers
#   90853a68  6 repos   dotnet/rust templates, gate, lighthouse-pr-events,
#                       plan-api, qa-canary
#   7c77ef19  2 repos   angular template, ship-proven-portal
#   16d33b51  1 repo    auth-service
#   1acf4dd3  1 repo    maestro
#
# None of the differences were repo-specific logic. They were age. Two fixes
# made in one afternoon reached four repos and six repos respectively, and the
# gap between them is where bugs live: the truncating-tail defect — a failing
# check reporting only its last three lines, so the diagnosis it had already
# written was thrown away — sat in all fourteen for months and was fixed in
# three.
#
# What it does, and the three outcomes it distinguishes:
#
#   exit 0   pass
#   exit 77  SKIP — the scenario does not apply in this environment. Counted as
#            neither pass nor fail, with the reason carried into results.json,
#            because the same packs run against a PR preview AND against
#            staging and some scenarios only exist in one. A preview-only check
#            exiting 1 on staging sets Arrival.phase=Failed and turns the
#            qa-gate red for a service that is fine.
#   other    fail, and the check's WHOLE log is printed, not the three lines
#            kept for results.json
#
set -eo pipefail
# The directory holding this repo's 0X-*.sh checks. Defaults to ./end2end so
# an interactive run needs no argument; the pipeline passes it explicitly.
SCRIPT_DIR="$(cd "${1:-end2end}" && pwd)"
cd "$SCRIPT_DIR/.."

echo "[end2end] preview=${PREVIEW_URL:-<unset>} ns=${PREVIEW_NAMESPACE:-<unset>} pr=${PULL_NUMBER:-<unset>}"

results_file="$SCRIPT_DIR/results.json"
: > "$results_file.tmp"

tests_json="[]"
passed=0
failed=0
skipped=0

# Exit code a check uses to say "this scenario does not apply in this
# environment". The same packs run against a PR preview AND against staging
# (arrivals-observer dispatches them into a playwright-runner Job), and a
# preview-only scenario exiting 1 on staging sets Arrival.phase=Failed, which
# turns the qa-gate red on both GitOps promotion PRs for a service that is
# fine. That happened to leartech-go-service-template 0.1.117.
#
# 77 is sysexits.h territory and unused by these scripts, so it cannot
# collide with a real failure.
SKIP_EXIT=77

shopt -s nullglob
scripts=("$SCRIPT_DIR"/[0-9][0-9]-*.sh)
if [ ${#scripts[@]} -eq 0 ]; then
  summary="no 0X-*.sh scripts found"
else
  for script in "${scripts[@]}"; do
    # Strip the leading [0-9][0-9]- ordering prefix so the test name in
    # results.json is the human-readable identity (e.g. `smoke`), not the
    # incidental "alphabetical position" (e.g. `01-smoke`). The gate's
    # required-tests config (qa-management/required-tests/<service>.yaml)
    # references tests by the prefix-stripped name. Renumbering scripts
    # (01- → 04-) must not break the gate.
    name=$(basename "$script" .sh | sed 's/^[0-9][0-9]-//')
    log="$SCRIPT_DIR/$name.log"
    echo "[end2end] running $name"
    t0=$(date +%s%3N)
    rc_script=0
    bash "$script" >"$log" 2>&1 || rc_script=$?
    if [ "$rc_script" -eq 0 ]; then
      status="pass"; message="OK"
      passed=$((passed + 1))
    elif [ "$rc_script" -eq "$SKIP_EXIT" ]; then
      # Neither pass nor fail: the reason travels in the message so a skip
      # cannot be read as coverage.
      status="skip"
      message=$(tail -3 "$log" 2>/dev/null | tr '\n' ' ' | head -c 300)
      [ -z "$message" ] && message="(skipped, no reason given)"
      skipped=$((skipped + 1))
      echo "[end2end] SKIP $name: $message"
    else
      status="fail"
      # Print the whole log, not just the tail kept for results.json.
      #
      # 03-scope-enforcement reports one line per assertion and names which
      # token was admitted where. On 2026-09-13 it failed in CI and this
      # runner emitted only "PER-ROUTE SCOPE ENFORCEMENT BROKEN" -- the three
      # lines that say nothing about which direction broke. Diagnosing it
      # needed a port-forward and a hand re-run, which is the whole point of
      # writing checks that explain themselves.
      echo "[end2end] ---- $name failed, full log ----"
      sed 's/^/[end2end]   /' "$log" 2>/dev/null || echo "[end2end]   (no log)"
      echo "[end2end] ---- end $name ----"
      message=$(tail -3 "$log" 2>/dev/null | tr '\n' ' ' | head -c 300)
      [ -z "$message" ] && message="(no output)"
      failed=$((failed + 1))
    fi
    t1=$(date +%s%3N)
    dur=$((t1 - t0))
    tests_json=$(jq -c \
      --arg name "$name" \
      --arg status "$status" \
      --argjson duration "$dur" \
      --arg message "$message" \
      '. + [{name: $name, status: $status, duration_ms: $duration, message: $message}]' \
      <<< "$tests_json")
  done
  total=$((passed + failed))
  if [ "$skipped" -gt 0 ]; then
    summary="$passed/$total checks passed, $skipped skipped"
  else
    summary="$passed/$total checks passed"
  fi
fi

if [ $failed -eq 0 ] && [ "$passed" -eq 0 ] && [ "$skipped" -gt 0 ]; then
  # Every check skipped: nothing applicable ran, which is not the same as
  # everything passing. Reported as a pass so a staging pack of preview-only
  # checks does not fail a promotion -- but the summary has to say it, or a
  # green tick means "verified" and "not verified" at the same time. That
  # ambiguity is what this estate keeps paying for.
  success=true
  summary="$summary — nothing applicable ran in this environment"
elif [ $failed -eq 0 ] && [ ${#scripts[@]} -gt 0 ]; then
  success=true
elif [ ${#scripts[@]} -eq 0 ]; then
  # Script directory exists but empty — catalog will treat this as PASS
  # with a warning row. Consumers should either remove end2end/ entirely
  # (catalog will post the "not configured" note) or add real checks.
  success=true
else
  success=false
fi

# metadata.doc — markdown the suite emits, rendered into the PR comment by the
# catalog end2end task under "What this run measured". Emitted by the run that
# measured it, so it cannot drift from the assertions.
doc=""
shopt -s nullglob
doc_files=("$SCRIPT_DIR"/doc/*.md)
if [ ${#doc_files[@]} -gt 0 ]; then
  doc=$(cat "${doc_files[@]}")
  echo "[end2end] collected ${#doc_files[@]} doc fragment(s) for the PR comment"
fi

jq -n \
  --argjson success "$success" \
  --arg summary "$summary" \
  --argjson tests "$tests_json" \
  --arg preview_url "${PREVIEW_URL:-}" \
  --arg version "${VERSION:-}" \
  --arg doc "$doc" \
  '{
    success:  $success,
    summary:  $summary,
    tests:    $tests,
    metadata: ({ preview_url: $preview_url, version: $version }
               + (if $doc == "" then {} else { doc: $doc } end))
  }' > "$results_file"

echo "[end2end] results.json:"
cat "$results_file"
