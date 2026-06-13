# promote-cluster-suffixed unit tests

Fixture-driven tests for the chart-update block of
`tasks/release/promote-cluster-suffixed.yaml`.

## Why these tests exist

`promote-cluster-suffixed` runs once per release pipeline on every consuming
repo (~30 services + the agent-set). Bugs here don't surface until a release,
where they corrupt the GitOps PR — late, expensive, and across multiple
clusters at once.

Originally the script only bumped `charts/$REPO_NAME`. Multi-chart repos
(e.g. `leartech-maestro-service` ships a service chart + a CRD-resource
chart for downstream consumers) broke at `helm-release` because the second
chart's `Chart.yaml` still carried `0.0.1`.

The fix iterates over `charts/*/`. The tests below pin the contract:
single-chart repos behave exactly as before (backward-compat for ~30
consumers) and multi-chart repos get every chart bumped.

## How the harness works

`run-tests.sh`:

1. **Extracts** the shell script from `promote-cluster-suffixed.yaml` using
   `yq` (Python `yq` package, mode-compatible with mikefarah/yq for our
   read-only purposes — we only call `.spec.pipelineSpec.tasks[0]...`).
2. **Trims** the script to just the chart-update block. The `git tag` /
   `git push` / `jx changelog` lines below it are out of scope for these
   tests — they're side-effects the release pipeline owns end-to-end, not
   unit-testable in isolation without a real registry.
3. **Stubs** `jx gitops yset` with a tiny Python helper that actually
   mutates the target YAML (so post-conditions are observable). Stubs
   `git` and `source .jx/variables.sh` with no-ops via a wrapper that
   pre-exports the env vars.
4. **Runs** each fixture scenario in its own temp dir, then asserts the
   resulting chart state.

POSIX-sh only — same shell shape as the real Tekton step (alpine sh).

## Running

```sh
sh test/promote-cluster-suffixed/run-tests.sh
```

Returns exit 0 if all scenarios pass; non-zero on the first failure with
a diff between expected and observed YAML.

## Scenarios covered

| # | Scenario | Expected |
|---|---|---|
| 1 | Single chart `charts/$REPO_NAME` with image | version + appVersion bumped; image.repository + image.tag set |
| 2 | Multi-chart: service (has image) + CRD chart (no image) | both Chart.yamls bumped; only service values.yaml gets image refs |
| 3 | No `charts/` directory | log line, no error |
| 4 | Empty `charts/` directory | log line, no error |
| 5 | `charts/X/` with no Chart.yaml | skip with log, no error |
