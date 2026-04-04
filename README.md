# Leartech Pipeline Catalog

Shared Tekton pipeline tasks for JX3 clusters. App repos reference these tasks via the `uses:` directive — no code duplication, single source of truth.

## Catalog Tasks

| Task | Trigger | What it does |
|------|---------|-------------|
| `tasks/ai-review/pullrequest.yaml` | PR | Multi-LLM AI code review (Claude + DeepSeek + Ollama), 0-100 scoring |
| `tasks/ai-review/feedback.yaml` | `/ai-feedback` comment | Process review feedback for training data |
| `tasks/security-scan/pullrequest.yaml` | PR | Gitleaks (secrets) + Semgrep (SAST) |
| `tasks/security-scan/image-scan.yaml` | PR | Grype dependency vulnerability scan |
| `tasks/security-scan/dynamic/pullrequest.yaml` | PR | Nuclei + Nikto + Nmap against live preview environment |
| `tasks/helm/release.yaml` | Merge to main | Helm release with cosign image signing |
| `tasks/tools/preview-copy-secrets.yaml` | PR | Copy secrets to preview namespaces |

## Architecture

```
App repo (.lighthouse/jenkins-x/)     Catalog (this repo)              Docker image
┌─────────────────────────┐         ┌──────────────────────┐        ┌─────────────────────┐
│ security-scan/           │  uses:  │ tasks/security-scan/  │  runs  │ security-tools:latest│
│   pullrequest.yaml      │───────▶│   pullrequest.yaml    │──────▶│   /app/static-scan.sh│
│   (thin wrapper)        │         │   (orchestration)     │        │   /app/image-scan.sh │
│                         │         │                      │        │   /app/dynamic-scan.sh│
│ ai-review/              │  uses:  │ tasks/ai-review/      │  runs  │ ai-review-worker     │
│   pullrequest.yaml      │───────▶│   pullrequest.yaml    │──────▶│   /app/review.py     │
└─────────────────────────┘         └──────────────────────┘        └─────────────────────┘
```

- **App repos** have thin wrapper YAMLs (< 20 lines each) — only `uses:` references, no inline logic
- **This catalog** has the orchestration — which images to run, what env vars to pass, step ordering
- **Docker images** have the actual scan/review logic as testable shell scripts

## How `uses:` Works

JX3 Lighthouse resolves `uses:` references at pipeline execution time:

```yaml
# In the app repo: thin wrapper
stepTemplate:
  image: uses:mikelear/leartech-pipeline-catalog/tasks/security-scan/pullrequest.yaml@main
steps:
- name: ""    # ← placeholder: catalog steps are injected here
```

The `stepTemplate.image` points to a catalog task file. Lighthouse fetches it and injects the steps. The empty step (`name: ""`) is required as a placeholder for the injection.

---

## Adding Security Scans to a Repo

### Step 1: Create the thin wrapper files

Create `.lighthouse/jenkins-x/security-scan/` in your repo with these files:

**`pullrequest.yaml`** — Static scan (Gitleaks + Semgrep):
```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  creationTimestamp: null
  name: security-scan
spec:
  pipelineSpec:
    tasks:
    - name: security-scan
      resources: {}
      taskSpec:
        metadata: {}
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/security-scan/pullrequest.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  podTemplate: {}
  serviceAccountName: tekton-bot
  timeout: 30m0s
status: {}
```

**`image-scan.yaml`** — Dependency scan (Grype):
```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  creationTimestamp: null
  name: image-scan
spec:
  pipelineSpec:
    tasks:
    - name: image-scan
      resources: {}
      taskSpec:
        metadata: {}
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/security-scan/image-scan.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  podTemplate: {}
  serviceAccountName: tekton-bot
  timeout: 30m0s
status: {}
```

**`dynamic/pullrequest.yaml`** — Live preview scan (Nuclei + Nikto + Nmap):
```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  creationTimestamp: null
  name: dynamic-security-scan
spec:
  pipelineSpec:
    tasks:
    - name: dynamic-scan
      resources: {}
      taskSpec:
        metadata: {}
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/security-scan/dynamic/pullrequest.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  podTemplate: {}
  serviceAccountName: tekton-bot
  timeout: 30m0s
status: {}
```

### Step 2: Add triggers

Update `.lighthouse/jenkins-x/triggers.yaml`:

```yaml
apiVersion: config.lighthouse.jenkins-x.io/v1alpha1
kind: TriggerConfig
spec:
  presubmits:
  - name: pr
    context: "pr"
    always_run: true
    optional: false
    source: "pullrequest.yaml"

  # AI Code Review
  - name: ai-code-review
    context: "ai-review"
    always_run: true
    optional: true
    source: "ai-review/pullrequest.yaml"

  # Static Security Scan (Gitleaks + Semgrep)
  - name: security-scan
    context: "security-scan"
    always_run: true
    optional: true
    source: "security-scan/pullrequest.yaml"

  # Dependency Scan (Grype)
  - name: image-scan
    context: "image-scan"
    always_run: true
    optional: true
    source: "security-scan/image-scan.yaml"

  # Dynamic Scan (Nuclei + Nikto + Nmap against preview)
  - name: dynamic-security-scan
    context: "dynamic-scan"
    always_run: true
    optional: true
    source: "security-scan/dynamic/pullrequest.yaml"

  postsubmits:
  - name: release
    context: "release"
    source: "release.yaml"
    branches:
    - ^main$
    - ^master$
```

### Step 3: That's it

Push to main. Every PR will trigger all scans on both clusters. Results are posted as PR comments with `[gcp]`/`[az]` cluster tags.

---

## Testing

There are two levels of testing, designed to avoid the slow push → PR → wait → check cycle.

### Level 1: Script Testing (fast, no Lighthouse)

Test individual scan scripts directly against the `pipeline-test` namespace. This namespace is permanently deployed on both clusters with an nginx endpoint and test data.

```bash
# Clone this repo
git clone https://github.com/mikelear/leartech-pipeline-catalog.git
cd leartech-pipeline-catalog

# See all available commands
make -f test/Makefile
```

This prints:
```
  Leartech Pipeline Catalog — Test Harness
  =========================================

  Tests scan scripts against the pipeline-test namespace.
  Dry-run mode: prints formatted PR comments, no API calls.

  Usage:  make -f test/Makefile <target> [AZURE=1]

  Targets:
    test-static     Run Gitleaks + Semgrep against test data
    test-image      Run Grype dependency scan against test data
    test-dynamic    Run Nuclei + Nikto + Nmap against pipeline-test nginx
    test-all        Run all scan tests sequentially

    logs TASK=x     Tail logs for a test (x = static, image, or dynamic)
    clean           Delete all test pods

  Options:
    AZURE=1         Run against Azure cluster (modern-burro-admin context)

  Examples:
    make -f test/Makefile test-static            # GCP static scan
    make -f test/Makefile test-dynamic AZURE=1   # Azure dynamic scan
    make -f test/Makefile test-all               # All scans on GCP
    make -f test/Makefile logs TASK=dynamic      # Watch dynamic scan logs
```

Run individual tests:
```bash
make -f test/Makefile test-static             # GCP
make -f test/Makefile test-dynamic AZURE=1    # Azure
make -f test/Makefile test-all                # All scans
make -f test/Makefile clean                   # Clean up
```

**What this tests:**
- Scan scripts work (`/app/static-scan.sh`, `/app/dynamic-scan.sh`, etc.)
- Scanner tools work (Gitleaks, Semgrep, Nuclei, Nikto, Nmap, Grype)
- PR comment formatting (printed to stdout in dry-run mode — PR=0, no API calls)
- Internal service DNS resolution (Nmap scans `test-app` in the namespace)

**What this does NOT test:**
- Lighthouse trigger resolution
- `uses:` directive step injection
- Real PR comment posting
- Preview namespace creation/cleanup
- Multi-cluster behaviour

### Level 2: Full End-to-End PR Test

Test the complete pipeline including Lighthouse, `uses:` resolution, preview deployment, and PR comment posting. This requires a real PR.

```bash
# Create a test branch with intentionally bad code
cd /path/to/future-lending-ui
git checkout -b feat/scan-test
# Add a file with known vulnerabilities (hardcoded secrets, eval, innerHTML)
git add . && git commit -m "feat: test component"
git push -u origin feat/scan-test

# Create PR
gh pr create --title "Security scan test" --body "Testing all scan checks"
```

**Expected results on both clusters:**

| Check | Context | Expected |
|-------|---------|----------|
| Build + Preview | `pr` | success (preview deployed) |
| AI Review | `ai-review` | failure (bad code flagged) |
| Static Scan | `security-scan` | failure (secrets + SAST findings) |
| Dependency Scan | `image-scan` | success or failure (depends on deps) |
| Dynamic Scan | `dynamic-scan` | pass/review (scans live preview) |

Each check posts a formatted PR comment with `[gcp]` or `[az]` cluster tag.

**Retrigger a single check:**
```bash
gh pr comment <PR_NUMBER> --body "/test security-scan"
gh pr comment <PR_NUMBER> --body "/test dynamic-security-scan"
gh pr comment <PR_NUMBER> --body "/test ai-code-review"
```

### pipeline-test Namespace

Both clusters have a permanent `pipeline-test` namespace with:

| Resource | Purpose |
|----------|---------|
| `test-app` Deployment | nginx serving test HTML |
| `test-app` Service | ClusterIP service for Nmap scanning |
| Ingress | `pipeline-test.{jx\|az}.leartech.com` for external scan testing |
| `test-data` ConfigMap | Sample diffs, bad code, vulnerable lockfiles |
| `test-content` ConfigMap | HTML content served by nginx |

---

## Multi-Cluster Support

All tasks read `CLUSTER_ID` from the `ai-review-cluster-config` ConfigMap (set per cluster: `gcp` or `az`). PR comments are tagged:

```
## :shield: Security Scan: **FAIL** `[gcp]`
## :shield: Dynamic Security Scan: **Review Recommended** `[az]`
```

Both clusters run independently — you see separate comments from each, making it easy to compare.

## Scan Scripts

All scan logic lives in shell scripts inside the `ghcr.io/mikelear/security-tools` Docker image (BlackArch-based, all tools via pacman):

| Script | What it does |
|--------|-------------|
| `/app/static-scan.sh` | Gitleaks + Semgrep, formats and posts results |
| `/app/image-scan.sh` | Grype dependency scan, formats and posts results |
| `/app/dynamic-scan.sh` | Nuclei + Nikto + Nmap + egress isolation test |
| `/app/post-scan-comment.sh` | Shared utility: formats markdown and posts to GitHub PR |

Scripts accept `--pr 0 --token ""` for dry-run mode (prints comment, no API call).

## Related Repos

| Repo | Purpose |
|------|---------|
| [leartech-dockerfiles](https://github.com/mikelear/leartech-dockerfiles) | security-tools + ai-review-worker Docker images |
| [leartech-security-reports](https://github.com/mikelear/leartech-security-reports) | Automated scan findings (CronJob issues) + incident runbook |
| [jx3-pipeline-catalog](https://github.com/mikelear/jx3-pipeline-catalog) | Fork of JX3 catalog with cluster-suffixed tags |
| [jx-build-cluster-gsm](https://github.com/mikelear/jx-build-cluster-gsm) | GCP cluster GitOps (Kyverno, security-scans CronJobs) |
| [jx-build-cluster-akv](https://github.com/mikelear/jx-build-cluster-akv) | Azure cluster GitOps |
