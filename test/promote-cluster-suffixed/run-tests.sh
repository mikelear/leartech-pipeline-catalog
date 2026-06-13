#!/usr/bin/env sh
# Fixture-driven tests for the chart-update block of
# tasks/release/promote-cluster-suffixed.yaml.
#
# See test/promote-cluster-suffixed/README.md for the scenario list.
#
# Run:    sh test/promote-cluster-suffixed/run-tests.sh
# Exit:   0 on all-pass, non-zero with a diff on first failure.
#
# Dependencies: python3 (PyYAML), and a `yq` either of:
#   - kislyuk/yq         (Python pip package — what CI uses for unit lint)
#   - mikefarah/yq       (Go binary — what the prod task uses)
# This script only ever does `yq read` (no `yq eval`) so both flavours work.

set -eu

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
TASK_YAML="$ROOT_DIR/tasks/release/promote-cluster-suffixed.yaml"

if [ ! -f "$TASK_YAML" ]; then
  echo "FATAL: $TASK_YAML not found"
  exit 2
fi

# ---------------------------------------------------------------------------
# Extract the chart-update block from the YAML.
#
# We use Python (PyYAML is in every CI image we run) rather than yq so the
# extraction logic is the same on every developer laptop. The chart-update
# block is delimited by:
#   start: line beginning with `if [ -d charts ]`
#   end:   line beginning with `TAG_SUFFIX=""`
# These markers are stable contract surface between the task and these tests
# — if you rename them in the task, update the slice below too.
# ---------------------------------------------------------------------------
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

python3 - "$TASK_YAML" "$WORK_DIR/chart-block.sh" <<'PYEOF'
import sys
import yaml

with open(sys.argv[1]) as fp:
    doc = yaml.safe_load(fp)

steps = doc["spec"]["pipelineSpec"]["tasks"][0]["taskSpec"]["steps"]
step = next(s for s in steps if s.get("name") == "promote-cluster-suffixed")
script = step["script"]

lines = script.splitlines()
start = next(i for i, ln in enumerate(lines) if ln.lstrip().startswith("if [ -d charts ]"))
end = next(i for i, ln in enumerate(lines) if ln.lstrip().startswith("TAG_SUFFIX="))

block = "\n".join(lines[start:end])
with open(sys.argv[2], "w") as fp:
    fp.write(block + "\n")
PYEOF

# ---------------------------------------------------------------------------
# Stub `jx gitops yset` with a Python implementation that actually mutates
# the target file, so we can observe post-conditions. We deliberately do NOT
# pretend to be the real jx — we only support the `gitops yset -p PATH -v VALUE -f FILE`
# invocation shape used by the task.
# ---------------------------------------------------------------------------
mkdir -p "$WORK_DIR/bin"
cat > "$WORK_DIR/bin/jx" <<'STUBEOF'
#!/usr/bin/env python3
import sys
import yaml

# Expected invocation: jx gitops yset -p PATH -v VALUE -f FILE
argv = sys.argv[1:]
if argv[:2] != ["gitops", "yset"]:
    sys.stderr.write("test stub: unexpected jx invocation: %r\n" % argv)
    sys.exit(2)
argv = argv[2:]

path = value = file_ = None
i = 0
while i < len(argv):
    if argv[i] == "-p":
        path = argv[i + 1]
        i += 2
    elif argv[i] == "-v":
        value = argv[i + 1]
        i += 2
    elif argv[i] == "-f":
        file_ = argv[i + 1]
        i += 2
    else:
        sys.stderr.write("test stub: unknown flag: %s\n" % argv[i])
        sys.exit(2)

with open(file_) as fp:
    doc = yaml.safe_load(fp) or {}

# Set nested key by dotted path. Real jx supports more (sequences, etc.) but
# the promote-cluster-suffixed task only uses simple dotted paths, so this
# is a faithful slice.
parts = path.split(".")
cursor = doc
for key in parts[:-1]:
    cursor = cursor.setdefault(key, {})
cursor[parts[-1]] = value

with open(file_, "w") as fp:
    yaml.safe_dump(doc, fp, default_flow_style=False, sort_keys=False)
STUBEOF
chmod +x "$WORK_DIR/bin/jx"

# Real script runs `git add * || true; git commit ... ; git tag ...; git push ...`
# AFTER our slice ends. We've trimmed to the chart-update block so git
# shouldn't be invoked at all — but provide a stub anyway in case the slice
# ever shifts. Same for `jx changelog` (re-uses the jx stub above; jx stub
# will exit 2 on unknown subcommand, which is what we want as a guardrail).
cat > "$WORK_DIR/bin/git" <<'STUBEOF'
#!/usr/bin/env sh
echo "test stub: git $* (no-op)" >&2
exit 0
STUBEOF
chmod +x "$WORK_DIR/bin/git"

export PATH="$WORK_DIR/bin:$PATH"
export VERSION="1.2.3"
export DOCKER_REGISTRY="registry.example.com"
export DOCKER_REGISTRY_ORG="testorg"
export APP_NAME="testapp"
export REPO_NAME="testapp"

# Each fixture builder runs in its own subdir, so failures are inspectable
# under $WORK_DIR/<scenario> after the script exits.
SCENARIOS_PASSED=0
SCENARIOS_FAILED=0

# read_yaml <file> <dotted.path> — uses Python to extract a value.
read_yaml() {
  python3 - "$1" "$2" <<'PYEOF'
import sys, yaml
with open(sys.argv[1]) as fp:
    doc = yaml.safe_load(fp) or {}
cursor = doc
for key in sys.argv[2].split("."):
    if not isinstance(cursor, dict) or key not in cursor:
        print("__MISSING__")
        sys.exit(0)
    cursor = cursor[key]
print(cursor)
PYEOF
}

assert_eq() {
  # assert_eq <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    printf '    ok    %s = %s\n' "$1" "$2"
  else
    printf '    FAIL  %s\n          expected: %s\n          actual:   %s\n' "$1" "$2" "$3"
    SCENARIO_FAIL=1
  fi
}

run_scenario() {
  scenario_name="$1"
  scenario_dir="$WORK_DIR/$scenario_name"
  mkdir -p "$scenario_dir"
  echo ""
  echo "--- scenario: $scenario_name ---"
  SCENARIO_FAIL=0
  (
    cd "$scenario_dir"
    # Build fixture state (caller-provided function)
    "fixture_$scenario_name"
    # Run the extracted chart-update block. Use sh -e so any unhandled
    # error fails the test (mirrors the real task's `set -e`).
    sh -e "$WORK_DIR/chart-block.sh" 2>&1 | sed 's/^/      /'
  )
  # Assertions (caller-provided function)
  "assert_$scenario_name" "$scenario_dir"
  if [ "$SCENARIO_FAIL" = "0" ]; then
    SCENARIOS_PASSED=$((SCENARIOS_PASSED + 1))
    echo "  PASS  $scenario_name"
  else
    SCENARIOS_FAILED=$((SCENARIOS_FAILED + 1))
    echo "  FAIL  $scenario_name"
  fi
}

# ---------------------------------------------------------------------------
# Scenario 1: single chart `charts/$REPO_NAME` with image
# ---------------------------------------------------------------------------
fixture_single_chart_with_image() {
  mkdir -p "charts/testapp"
  cat > charts/testapp/Chart.yaml <<EOF
apiVersion: v2
name: testapp
description: A test chart
version: 0.0.1
appVersion: 0.0.1
EOF
  cat > charts/testapp/values.yaml <<EOF
image:
  repository: oldreg/oldorg/oldapp
  tag: 0.0.0
replicas: 1
EOF
}
assert_single_chart_with_image() {
  d="$1"
  assert_eq "Chart.yaml.version"          "1.2.3"                                   "$(read_yaml "$d/charts/testapp/Chart.yaml" version)"
  assert_eq "Chart.yaml.appVersion"       "1.2.3"                                   "$(read_yaml "$d/charts/testapp/Chart.yaml" appVersion)"
  assert_eq "values.yaml.image.repository" "registry.example.com/testorg/testapp"   "$(read_yaml "$d/charts/testapp/values.yaml" image.repository)"
  assert_eq "values.yaml.image.tag"        "1.2.3"                                  "$(read_yaml "$d/charts/testapp/values.yaml" image.tag)"
}

# ---------------------------------------------------------------------------
# Scenario 2: multi-chart — service (with image) + CRD chart (no image)
# ---------------------------------------------------------------------------
fixture_multi_chart() {
  mkdir -p charts/foo-service charts/foo-crds
  cat > charts/foo-service/Chart.yaml <<EOF
apiVersion: v2
name: foo-service
version: 0.0.1
appVersion: 0.0.1
EOF
  cat > charts/foo-service/values.yaml <<EOF
image:
  repository: oldreg/oldorg/oldapp
  tag: 0.0.0
replicas: 2
EOF
  cat > charts/foo-crds/Chart.yaml <<EOF
apiVersion: v2
name: foo-crds
version: 0.0.1
appVersion: 0.0.1
EOF
  cat > charts/foo-crds/values.yaml <<EOF
# CRD-resource chart — ships CR YAML for consumers, no container image.
crds:
  enabled: true
EOF
}
assert_multi_chart() {
  d="$1"
  # Both charts: Chart.yaml version + appVersion bumped
  assert_eq "service.Chart.version"      "1.2.3" "$(read_yaml "$d/charts/foo-service/Chart.yaml" version)"
  assert_eq "service.Chart.appVersion"   "1.2.3" "$(read_yaml "$d/charts/foo-service/Chart.yaml" appVersion)"
  assert_eq "crds.Chart.version"         "1.2.3" "$(read_yaml "$d/charts/foo-crds/Chart.yaml" version)"
  assert_eq "crds.Chart.appVersion"      "1.2.3" "$(read_yaml "$d/charts/foo-crds/Chart.yaml" appVersion)"
  # Service: image refs updated
  assert_eq "service.values.image.repository" "registry.example.com/testorg/testapp" "$(read_yaml "$d/charts/foo-service/values.yaml" image.repository)"
  assert_eq "service.values.image.tag"        "1.2.3"                                "$(read_yaml "$d/charts/foo-service/values.yaml" image.tag)"
  # CRDs: image.* MUST be absent (no image: block touched)
  assert_eq "crds.values.image (absent)" "__MISSING__" "$(read_yaml "$d/charts/foo-crds/values.yaml" image)"
}

# ---------------------------------------------------------------------------
# Scenario 3: no charts/ directory
# ---------------------------------------------------------------------------
fixture_no_charts_dir() {
  :  # nothing to set up
}
assert_no_charts_dir() {
  d="$1"
  if [ -d "$d/charts" ]; then
    echo "    FAIL  charts/ should not exist"
    SCENARIO_FAIL=1
  else
    echo "    ok    charts/ correctly absent"
  fi
}

# ---------------------------------------------------------------------------
# Scenario 4: empty charts/ directory
# ---------------------------------------------------------------------------
fixture_empty_charts_dir() {
  mkdir -p charts
}
assert_empty_charts_dir() {
  d="$1"
  if [ -d "$d/charts" ]; then
    # Should still be empty post-run
    if [ -z "$(ls -A "$d/charts")" ]; then
      echo "    ok    charts/ remained empty"
    else
      echo "    FAIL  charts/ unexpectedly populated"
      SCENARIO_FAIL=1
    fi
  else
    echo "    FAIL  charts/ vanished"
    SCENARIO_FAIL=1
  fi
}

# ---------------------------------------------------------------------------
# Scenario 5: charts/X/ exists but has no Chart.yaml — skip with log
# ---------------------------------------------------------------------------
fixture_chart_dir_without_chart_yaml() {
  mkdir -p charts/incomplete
  cat > charts/incomplete/values.yaml <<EOF
some: value
EOF
}
assert_chart_dir_without_chart_yaml() {
  d="$1"
  # values.yaml should remain untouched
  expected="some: value"
  actual="$(cat "$d/charts/incomplete/values.yaml")"
  assert_eq "incomplete/values.yaml untouched" "$expected" "$actual"
  # Chart.yaml should still not exist
  if [ ! -f "$d/charts/incomplete/Chart.yaml" ]; then
    echo "    ok    no Chart.yaml created"
  else
    echo "    FAIL  Chart.yaml unexpectedly created"
    SCENARIO_FAIL=1
  fi
}

# ---------------------------------------------------------------------------
# Run them all
# ---------------------------------------------------------------------------
run_scenario single_chart_with_image
run_scenario multi_chart
run_scenario no_charts_dir
run_scenario empty_charts_dir
run_scenario chart_dir_without_chart_yaml

echo ""
echo "==================================="
echo "passed: $SCENARIOS_PASSED"
echo "failed: $SCENARIOS_FAILED"
echo "==================================="
[ "$SCENARIOS_FAILED" = "0" ]
