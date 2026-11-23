package main

import (
	"testing"
)

// THE DEFECT: a step writes ${VAR:?...} and never declares VAR. The shell exits at that line,
// so the step does its job only when the variable happens to be set — and never when its own
// check fails.
//
// Measured on leartech-console-api#37: lint failed at comment-gate, the report step died with
// "line 10: CLUSTER_ID: parameter null or not set", and the PR showed only Lighthouse's generic
// table. Three tasks shipped it (go-lint, chart-standard, values-standard) and none of them is
// distinguishable on a passing PR, because a report step only runs on failure.
//
// proven-by: TestEnvRefsFindsAFatalVarThatIsNeverDeclared
func TestEnvRefsFindsAFatalVarThatIsNeverDeclared(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: report
          script: |
            #!/bin/bash
            set -u
            printf 'FAIL [%s]\n' "${CLUSTER_ID:?}"
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code == 0 {
		t.Fatal("a step using ${CLUSTER_ID:?} with no declaration passed")
	}
}

// ...and declaring it on the step fixes it. That is the whole fix, and it is why the three
// offenders were a copy-paste omission rather than a design problem: every sibling task does it.
//
// proven-by: TestEnvRefsAcceptsADeclaredVar
func TestEnvRefsAcceptsADeclaredVar(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: report
          env:
          - name: CLUSTER_ID
            valueFrom:
              configMapKeyRef:
                name: jx-cluster-config
                key: CLUSTER_ID
                optional: true
          script: |
            #!/bin/bash
            set -u
            printf 'FAIL [%s]\n' "${CLUSTER_ID:?}"
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code != 0 {
		t.Fatal("a declared variable was rejected")
	}
}

// A declaration on the taskSpec's stepTemplate counts: that is the shape most tasks use, and
// checking only the step's own env would report every one of them.
//
// proven-by: TestEnvRefsAcceptsAStepTemplateDeclaration
func TestEnvRefsAcceptsAStepTemplateDeclaration(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        stepTemplate:
          env:
          - name: CLUSTER_ID
            value: gcp
        steps:
        - name: lint
          script: |
            #!/bin/sh
            echo "${CLUSTER_ID:?}"
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code != 0 {
		t.Fatal("a stepTemplate declaration was not counted")
	}
}

// envFrom does NOT count. jx-boot-job-env-vars is optional: true and carries only DEV_ and
// JX_REPOSITORY_ credentials, so an envFrom reference is a hope rather than a declaration —
// which is exactly the assumption that let the three offenders look configured.
//
// proven-by: TestEnvRefsDoesNotTrustAnOptionalEnvFrom
func TestEnvRefsDoesNotTrustAnOptionalEnvFrom(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        stepTemplate:
          envFrom:
          - secretRef:
              name: jx-boot-job-env-vars
              optional: true
        steps:
        - name: report
          script: |
            #!/bin/bash
            echo "${CLUSTER_ID:?}"
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code == 0 {
		t.Fatal("an optional envFrom was treated as a declaration")
	}
}

// A comment that mentions the pattern is not a use of it — every guard in this catalog has had
// to learn that, and this one's own explanation quotes the syntax.
//
// proven-by: TestEnvRefsIgnoresComments
func TestEnvRefsIgnoresComments(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: report
          script: |
            #!/bin/bash
            # never write "${CLUSTER_ID:?}" without declaring it
            echo ok
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code != 0 {
		t.Fatal("a comment was counted as a use")
	}
}

// A `${VAR}` with no `:?` has a fallback of empty, so it is not this check's business: it
// degrades rather than dying, and plenty of legitimate steps rely on that.
//
// proven-by: TestEnvRefsIgnoresANonFatalReference
func TestEnvRefsIgnoresANonFatalReference(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: report
          script: |
            #!/bin/bash
            echo "cluster ${CLUSTER_ID} (may be empty)"
`,
	})
	steps, errs := loadSteps(root)
	if code := runEnvRefs(steps, errs); code != 0 {
		t.Fatal("a non-fatal reference was reported")
	}
}
