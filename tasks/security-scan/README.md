# Security Scan Catalog Tasks

PR-triggered security scanning tasks that post results back as formatted PR comments with multi-cluster tagging.

## Tasks

### `pullrequest.yaml` — Static Security Scan
Runs on PR. Uses `ghcr.io/mikelear/security-tools` image.
- **Gitleaks**: scans working tree for leaked secrets
- **Semgrep**: static analysis with auto-config rules
- Posts combined results as PR comment with severity table
- Fails pipeline on leaked secrets or semgrep errors

### `image-scan.yaml` — Dependency Vulnerability Scan
Runs on PR. Uses `anchore/grype` image.
- **Grype**: scans repo dependencies (lockfiles, manifests) for CVEs
- Posts results as PR comment with severity breakdown
- Fails pipeline on critical vulnerabilities

## Multi-Cluster Support

All tasks read `CLUSTER_ID` from `ai-review-cluster-config` ConfigMap. PR comments are tagged with `[gcp]` or `[az]` so you can see which cluster ran the scan.

## Wiring Into a Repo

Add to `.lighthouse/jenkins-x/triggers.yaml`:

```yaml
- name: security-scan
  source: "leartech-pipeline-catalog"
  tasks:
  - name: security-scan
    taskRef:
      name: security-scan
    pipelineRef:
      name: pullrequest
```
