# AI Code Review Pipeline

Standalone AI code review that runs independently of build pipelines.

## How to enable on a repo

Add these files to your repo's `.lighthouse/jenkins-x/` directory:

### `.lighthouse/jenkins-x/ai-review/triggers.yaml`

```yaml
apiVersion: config.lighthouse.jenkins-x.io/v1alpha1
kind: TriggerConfig
spec:
  presubmits:
  - name: ai-code-review
    always_run: true
    source: "ai-review/pullrequest.yaml"
    max_concurrency: 1
  - name: ai-review-feedback
    always_run: false
    trigger: "/ai-feedback"
    rerun_command: "/ai-feedback"
    source: "ai-review/feedback.yaml"
    max_concurrency: 1
```

### `.lighthouse/jenkins-x/ai-review/pullrequest.yaml`

```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: ai-code-review
spec:
  pipelineSpec:
    tasks:
    - name: ai-review
      resources: {}
      taskSpec:
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/ai-review/pullrequest.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  serviceAccountName: tekton-bot
  timeout: 30m0s
```

### `.lighthouse/jenkins-x/ai-review/feedback.yaml`

```yaml
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: ai-review-feedback
spec:
  pipelineSpec:
    tasks:
    - name: process-feedback
      resources: {}
      taskSpec:
        stepTemplate:
          image: uses:mikelear/leartech-pipeline-catalog/tasks/ai-review/feedback.yaml@main
          name: ""
          resources: {}
          workingDir: /workspace/source
        steps:
        - name: ""
          resources: {}
  serviceAccountName: tekton-bot
  timeout: 30m0s
```

## Feedback commands

Comment on a PR to interact with the AI review:

| Command | What it does |
|---|---|
| `/ai-feedback approve` | Override FAIL verdict — must include reason |
| `/ai-feedback reject` | Override PASS verdict — must include reason |
| `/ai-feedback context: <text>` | Add context and re-run review with it |
| `/ai-feedback re-review` | Re-run the review (e.g. after prompt changes) |

### Examples

```
/ai-feedback approve
This pattern is intentional — we use string concatenation here because
the values are from a trusted internal enum, not user input.
```

```
/ai-feedback context: Our team always uses this pattern for Terraform modules.
The hardcoded values are intentional for this specific bootstrap resource.
```

```
/ai-feedback re-review
```

All feedback is captured in the `leartech-llm-training-data` Git repo
and ChromaDB for RAG context in future reviews.
