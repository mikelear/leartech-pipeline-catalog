# Leartech Pipeline Catalog

Shared Tekton pipeline tasks for JX3 clusters. App repos reference these tasks via the `uses:` directive — no code duplication, single source of truth.

## Catalog Tasks

| Task | Trigger | What it does |
|------|---------|-------------|
| `tasks/ai-review/pullrequest.yaml` | PR | Multi-LLM AI code review (Claude + DeepSeek + Ollama), 0-100 scoring |
| `tasks/ai-review/feedback.yaml` | `/ai-feedback` comment | Process review feedback for training data |
| `tasks/security-scan/pullrequest.yaml` | PR | Gitleaks (secrets) + Semgrep (SAST) |
| `tasks/security-scan/image-scan.yaml` | PR | Grype dependency vulnerability scan |
| `tasks/security-scan/dynamic/pullrequest.yaml` | PR | External scan (Nikto + Nuclei through ingress) + Internal scan (Nmap + egress isolation inside namespace) |
| `tasks/python-build-only/pullrequest.yaml` | PR | Python PR build (ruff + pytest + kaniko) **without** `jx preview create` — for services with cluster-scoped Helm resources or no end-user-facing reason for previews. See [tasks/python-build-only/README.md](tasks/python-build-only/README.md). |
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

## Canonical Go toolchain versions

Leartech Go services share a single source of truth for what CI runs — the
Tekton tasks in `tasks/go-lint/` and `tasks/go-test/` — and a matching set of
`make` targets in `go/leartech-go.mk` that a developer runs on their laptop.
CI and local invoke IDENTICAL commands with IDENTICAL config. If they ever
drift, this catalog is wrong.

### The canonical version

| Tool | Canonical version | Set in |
|------|-------------------|--------|
| `golangci-lint` | `2.12.2` | `go/leartech-go.mk` → `GOLANGCI_VERSION`; mirrored in `tasks/go-lint/pullrequest.yaml` step image `golangci/golangci-lint:v2.12.2` |

To bump the linter version:

1. Edit `GOLANGCI_VERSION` in `go/leartech-go.mk`.
2. Update the step image tag in `tasks/go-lint/pullrequest.yaml` in the SAME PR.
3. The parity test at `test/go/parity_test.sh` fails if a linter is added or
   removed without an intentional update — the version bump itself doesn't
   change the set, so no parity update is needed for a straight version bump.

### Using `leartech-go.mk` in a consumer repo

Two supported patterns, both equivalent from the CI perspective (same commands, same config):

**Pattern A — one-shot `make -f`**

```bash
# In your Go service repo
curl -fsSL https://raw.githubusercontent.com/mikelear/leartech-pipeline-catalog/main/go/leartech-go.mk -o leartech-go.mk
make -f leartech-go.mk lint          # curl base config + yq-merge + golangci-lint
make -f leartech-go.mk test-coverage # go test -race -coverpkg=./internal/... + delta-vs-base
make -f leartech-go.mk pre-push      # vet tidy-check build test-coverage lint vuln
```

**Pattern B — `include` from your Makefile**

```make
# Makefile in your Go service repo
include leartech-go.mk

# Repo-specific targets can go here; they don't collide with the canonical ones.
```

Then `make lint`, `make test-coverage`, `make pre-push` behave the same way.

### Consumer-tunable knobs

All variables in `leartech-go.mk` use `?=` so a repo can override before include:

| Variable | Default | What it controls |
|---|---|---|
| `GOLANGCI_VERSION` | `2.12.2` | Displayed for `make -f leartech-go.mk help`; the tool version you should install locally |
| `GOLANGCI_BASE_URL` | raw.githubusercontent.com/…/main/go/.golangci.base.yml | Where `lint-config` curls the base config from |
| `GOLANGCI_BASE_FILE` | (unset) | Optional local path to the base config; when set + exists, used INSTEAD of curl (for dogfooding within this repo) |
| `GOLANGCI_MERGED` | `.golangci.merged.yml` | Output path for the merged config |
| `COVERAGE_SCOPE` | `./internal/...` | `go test -coverpkg` scope |
| `COVERAGE_THRESHOLD` | `60.0` | Minimum acceptable total coverage % |
| `COVERAGE_DELTA_TOLERANCE` | `0.5` | Max allowed drop vs base coverage in pp (absorbs noise) |
| `PULL_BASE_REF` | `main` | Base branch for delta-vs-base check |

The Tekton go-test task also reads these from env vars — set them via
`env:` in the consumer repo's `.lighthouse/jenkins-x/pullrequest.yaml` and
they flow through to both CI and local without duplication.

### What `pre-push` runs

`pre-push` is the local equivalent of "everything CI would fail you on before
your PR opens":

```
vet → tidy-check → build → test-coverage → lint → vuln
```

Ordered cheap-first so a broken `go vet` fails in seconds instead of after
the ~90s coverage run.

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
    test-static      Run Gitleaks + Semgrep against test data
    test-image       Run Grype dependency scan against test data
    test-dynamic     Run both external + internal scans (full dynamic)
    test-external    Run Nikto + Nuclei through ingress (attacker view)
    test-internal    Run Nmap + egress test inside namespace
    test-all         Run all scan tests sequentially

    logs TASK=x      Tail logs (x = static, image, dynamic, external, internal)
    clean            Delete all test pods

  Options:
    AZURE=1          Run against Azure cluster (modern-burro-admin context)

  Examples:
    make -f test/Makefile test-static              # GCP static scan
    make -f test/Makefile test-external            # External scan (Nikto + Nuclei)
    make -f test/Makefile test-internal            # Internal scan (Nmap + egress)
    make -f test/Makefile test-dynamic             # Both external + internal
    make -f test/Makefile test-dynamic AZURE=1     # Azure dynamic scan
    make -f test/Makefile test-all                 # All scans on GCP
    make -f test/Makefile logs TASK=external       # Watch external scan logs
```

Run individual tests:
```bash
make -f test/Makefile test-external           # Attacker view (Nikto + Nuclei through ingress)
make -f test/Makefile test-internal           # Inside namespace (Nmap + egress isolation)
make -f test/Makefile test-dynamic            # Both phases
make -f test/Makefile test-all AZURE=1        # All scans on Azure
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
| `/app/external-scan.sh` | Nikto + Nuclei through ingress + TLS check (attacker view) |
| `/app/internal-scan.sh` | Nmap port scan + egress isolation test (inside namespace) |
| `/app/dynamic-scan.sh` | Wrapper: runs external + internal sequentially (for testing) |
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
