# Python Build-Only PR Pipeline

PR-time build pipeline for Python services that opt out of preview-deploy.

## When to use this

Use `python-build-only/pullrequest.yaml` instead of the standard Python
PR build for services that:

1. **Expose internal APIs to other platform services** (not to end users)
   — e.g. MCP SSE endpoints consumed by orch + agent, internal
   admin/operator services. These have no user-facing reason to deploy
   into a preview environment per PR.

2. **Have cluster-scoped Helm resources** (ClusterRole, ClusterRoleBinding,
   ValidatingWebhookConfiguration, CRDs) that would conflict with the
   preview namespace lifecycle.

The second case is the dangerous one. Cluster-scoped resources are NOT
garbage-collected when the preview namespace is torn down. The chain of
events:

```
PR #N opens
  → jx preview create
    → helm install ./charts/preview --namespace jx-<org>-<repo>-pr-N
      → creates ClusterRole "<chart>-<role>"  (cluster-scoped!)
PR #N closes
  → namespace deleted  (preview pods, services, configmaps all gone)
  → ClusterRole orphaned  (no owner ref to the namespace)
PR #N+1 opens
  → jx preview create
    → helm install ./charts/preview --namespace jx-<org>-<repo>-pr-(N+1)
      → tries to create the same ClusterRole
      → error: "already exists; cannot be imported into the current
        release: invalid ownership metadata"
      → preview-deploy fails
      → every subsequent PR red on `pr`
```

Captured as memory id=9 (`preview_helm_install_cluster_role_orphan`),
hit by `leartech-platform-mcps` PR #12 and PR #13 (2026-06-12) — the
canonical case that motivated this task.

## What this task does and does NOT

| Step              | Standard `python/release.yaml` | `python-build-only/pullrequest.yaml` |
|-------------------|--------------------------------|---------------------------------------|
| git-clone-pr      | ✓ (release uses non-PR clone) | ✓                                     |
| jx-variables      | ✓                              | ✓                                     |
| ruff format/check | inside build-python-test       | inside build-python-test              |
| pytest + coverage | inside build-python-test       | inside build-python-test              |
| kaniko build      | ✓                              | ✓                                     |
| helm-release      | ✓                              | —                                     |
| cosign-sign       | ✓                              | —                                     |
| **jx preview create / promote-jx-preview** | not in release | **DELIBERATELY OMITTED** |

The omission of `jx preview create` is the entire point.

## Companion pipelines remain unchanged

This task only owns the `pr` build pipeline. AI review, static security
scan (semgrep + gitleaks), and image scan (grype) continue to run as
SEPARATE pipelines triggered alongside `pr` from the consumer repo's
`.lighthouse/jenkins-x/triggers.yaml`. They are unaffected by this
change — consumer repos still get the full quality gate.

## How to enable on a repo

Edit `.lighthouse/jenkins-x/triggers.yaml` to point the `pr` presubmit
at this task instead of the standard python task:

```yaml
apiVersion: config.lighthouse.jenkins-x.io/v1alpha1
kind: TriggerConfig
spec:
  presubmits:
  - name: pr
    context: "pr"
    always_run: true
    optional: false
    source: "pullrequest.yaml"   # ← thin wrapper points at python-build-only

  # AI Code Review (unchanged)
  - name: ai-code-review
    context: "ai-review"
    always_run: true
    optional: true
    source: "ai-review/pullrequest.yaml"

  # Static Security Scan (unchanged)
  - name: security-scan
    context: "security-scan"
    always_run: true
    optional: true
    source: "security-scan/pullrequest.yaml"

  # Dependency / image scan (unchanged)
  - name: image-scan
    context: "image-scan"
    always_run: true
    optional: true
    source: "security-scan/image-scan.yaml"

  postsubmits:
  - name: release
    context: "release"
    source: "release.yaml"
    branches:
    - ^main$
```

Then create `.lighthouse/jenkins-x/pullrequest.yaml` as a thin wrapper:

```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  creationTimestamp: null
  name: pullrequest
spec:
  pipelineSpec:
    tasks:
    - name: from-build-pack
      resources: {}
      taskSpec:
        metadata: {}
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/python-build-only/pullrequest.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  serviceAccountName: tekton-bot
  timeout: 2h0m0s
status: {}
```

Release behaviour is unchanged — keep using
`tasks/python/release.yaml@main`.

## Adopting repos

| Repo                       | Reason                                       |
|----------------------------|----------------------------------------------|
| `leartech-platform-mcps`   | Canonical consumer. k8s-mcp ClusterRole.     |

When a new Python service adopts this task, add it to the table above
and reference the lesson id=9 motivation in its PR description so
future readers understand the choice.

## See also

- `tasks/python/release.yaml` — release pipeline (unchanged; this task
  is additive only).
- `tasks/security-scan/pullrequest.yaml` — semgrep + gitleaks scans.
- `tasks/security-scan/image-scan.yaml` — grype dependency scan.
- `tasks/ai-review/pullrequest.yaml` — multi-LLM AI code review.
- Memory id=9 — `preview_helm_install_cluster_role_orphan` (the
  motivating bug pattern).
