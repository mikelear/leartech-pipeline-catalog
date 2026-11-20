package main

import "testing"

// A phase that installs its tools at run time is the original bug: the tool lands in THIS
// step's container and is absent in the next one, whose failure then reads as a broken
// preview. proven-by: TestPhasesRejectsAPhaseThatInstallsAtRunTime
func TestPhasesRejectsAPhaseThatInstallsAtRunTime(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/end2end/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: end2end-await
          image: debian:12-slim
          script: |
            #!/bin/bash
            apt-get install -y curl kubectl
`,
	})
	if code := runPhases(root); code == 0 {
		t.Fatal("a phase installing its tools at run time passed")
	}
}

// The same phase in the checkers image, asserting its tools, passes.
func TestPhasesPassesAWellProvisionedPhase(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/end2end/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: end2end-env
          image: ghcr.io/mikelear/leartech-checkers:0.39.14
          script: |
            #!/bin/bash
            command -v curl kubectl
        - name: end2end-await
          image: ghcr.io/mikelear/leartech-checkers:0.39.14
          script: |
            #!/bin/bash
            kubectl get svc
            curl -sS "$PREVIEW_URL"
`,
	})
	if code := runPhases(root); code != 0 {
		t.Fatal("a correctly provisioned pair of phases was rejected")
	}
}

// A phase that waits on a preview without mentioning kubectl cannot resolve the Service or
// confirm the commit, so its "not ready" verdict is about its own blindness.
func TestPhasesRejectsAnAwaitWithNoKubectl(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/end2end/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: end2end-await
          image: ghcr.io/mikelear/leartech-checkers:0.39.14
          script: |
            #!/bin/bash
            curl -sS "$PREVIEW_URL"
`,
	})
	if code := runPhases(root); code == 0 {
		t.Fatal("an await phase with no kubectl passed")
	}
}

// A task that renders a chart and never judges it is a green check over half the question.
// proven-by: TestRendersFailsOnATaskThatRendersAndJudgesNothing
func TestRendersFailsOnATaskThatRendersAndJudgesNothing(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/helm/pullrequest.yaml": taskYAML("render", `#!/bin/bash
helm template rel charts/x > /tmp/render.yaml
`),
	})
	baseline := writeTree(t, map[string]string{"baseline.txt": ""})
	if code := runRenders(root, baseline+"/baseline.txt"); code == 0 {
		t.Fatal("a task that renders without judging passed")
	}
}

// ...and the same task, with a chartstd step, is fine.
func TestRendersPassesATaskThatJudges(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/helm/pullrequest.yaml": taskYAML("judge", `#!/bin/bash
helm template rel charts/x > /tmp/render.yaml
chartstd -store leartech-secrets -render /tmp/render.yaml
`),
	})
	baseline := writeTree(t, map[string]string{"baseline.txt": ""})
	if code := runRenders(root, baseline+"/baseline.txt"); code != 0 {
		t.Fatal("a task that renders and judges was rejected")
	}
}

// A baseline line whose task now judges is stale, and staleness is a failure: otherwise the
// list is a permanent licence and the next reader believes it still applies.
func TestRendersFailsOnAStaleBaselineLine(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/helm/pullrequest.yaml": taskYAML("judge", `#!/bin/bash
helm template rel charts/x > /tmp/render.yaml
chartstd -store leartech-secrets -render /tmp/render.yaml
`),
	})
	baseline := writeTree(t, map[string]string{"baseline.txt": "tasks/helm/pullrequest.yaml\n"})
	if code := runRenders(root, baseline+"/baseline.txt"); code == 0 {
		t.Fatal("a stale baseline line passed")
	}
}
