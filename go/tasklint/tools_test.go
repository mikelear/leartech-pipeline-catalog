package main

import (
	"os"
	"path/filepath"
	"testing"
)

const toolsDockerfile = "FROM alpine\nRUN set -eu; \\\n    for m in catalogtool othertool; do build; done\n\\\n    for b in catalogtool othertool; do verify; done\n"

// THE DEFECT: a step calls a catalog binary its image does not have. catalog#443 — the
// chart-standard render step ran in jx-boot (helm yes, chartstd no) and called
// `chartstd -list-toggles`, so the behavioural half of the gate had never run anywhere.
//
// proven-by: TestToolsFindsACatalogBinaryCalledFromTheWrongImage
func TestToolsFindsACatalogBinaryCalledFromTheWrongImage(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/jenkins-x/jx-boot:3.17.129
          name: render
          script: |
            #!/bin/bash
            catalogtool -tenant charts/x
`,
	})
	if code := runTools(root, baselineFor(t, "")); code == 0 {
		t.Fatal("a step calling a catalog binary from an image without it passed")
	}
}

// The same step in the checkers image passes: that image's own build asserts every binary it
// ships, so there is nothing for this check to add.
func TestToolsAcceptsTheCheckersImage(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/mikelear/leartech-checkers:0.48.2
          name: judge
          script: |
            #!/bin/bash
            catalogtool -tenant charts/x
`,
	})
	if code := runTools(root, baselineFor(t, "")); code != 0 {
		t.Fatal("a step in the checkers image was rejected")
	}
}

// ...and so does a step STAGED from a checkers step in the same task — the
// `provide-catalog-tools` pattern, which is why the prpost call sites in go-lint and go-test
// are legitimate from a golang image.
//
// proven-by: TestToolsAcceptsABinaryStagedByAPreviousStep
func TestToolsAcceptsABinaryStagedByAPreviousStep(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/mikelear/leartech-checkers:0.48.2
          name: provide
          script: |
            #!/bin/bash
            cp /usr/local/bin/catalogtool /workspace/bin/catalogtool
        - image: golang:1.27.4
          name: use
          script: |
            #!/bin/bash
            catalogtool -check
`,
	})
	if code := runTools(root, baselineFor(t, "")); code != 0 {
		t.Fatal("a binary staged by a previous step was reported as missing")
	}
}

// A step that BUILDS the binary is not calling an image's copy — end2end's `authtoken-build`
// writes a Go program and `go build`s it. The check does not try to detect that by looking for
// `go build` (a guess that could exempt a step doing both); it is a BASELINE decision, so the
// reason is on file.
//
// proven-by: TestToolsAcceptsAStepThatBuildsTheBinary
func TestToolsAcceptsAStepThatBuildsTheBinary(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: golang:1.27.4
          name: build-it
          script: |
            #!/bin/bash
            cat > "$work/main.go" <<'GO'
            package main
            GO
            go build -o "$work/catalogtool" .
            PATH="$work:$PATH" catalogtool -self-test
`,
	})
	if code := runTools(root, baselineFor(t, "")); code == 0 {
		t.Fatal("an unrecorded build-from-source step passed; it needs its reason on file")
	}
	waived := baselineFor(t,
		"tasks/demo/pullrequest.yaml::build-it::catalogtool|builds it from the embedded Go source\n")
	if code := runTools(root, waived); code != 0 {
		t.Fatal("a recorded build-from-source reason was rejected")
	}
}

// A mention is not a call. The chart-standard defect was named in a COMMENT in another step
// ("chartstd still refuses to pass an empty render"), and a check that fires on that is the
// counts-mentions defect every guard in this repo has paid for.
//
// proven-by: TestToolsIgnoresCommentMentions
func TestToolsIgnoresCommentMentions(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/jenkins-x/jx-boot:3.17.129
          name: lint
          script: |
            #!/bin/bash
            # catalogtool is the only place S1-S10 are enforced.
            helm lint charts/x   # not catalogtool, and not a call
`,
	})
	if code := runTools(root, baselineFor(t, "")); code != 0 {
		t.Fatal("a comment mention was counted as a call")
	}
}

// A baseline line whose step has since moved to the checkers image is stale, and staleness
// fails: leaving it implies the decision still stands.
//
// proven-by: TestToolsFailsOnAStaleBaselineLine
func TestToolsFailsOnAStaleBaselineLine(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, map[string]string{
		"tasks/demo/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/mikelear/leartech-checkers:0.48.2
          name: judge
          script: |
            #!/bin/bash
            catalogtool -tenant charts/x
`,
	})
	waived := baselineFor(t, "tasks/demo/pullrequest.yaml::judge::catalogtool|was in jx-boot\n")
	if code := runTools(root, waived); code == 0 {
		t.Fatal("a stale baseline line passed")
	}
}

// A Dockerfile with no build list is a failure, not a clean tree.
func TestToolsRefusesAnUnreadableToolList(t *testing.T) {
	root := writeToolTree(t, "FROM alpine\nRUN echo nothing\n", nil)
	if code := runTools(root, baselineFor(t, "")); code == 0 {
		t.Fatal("no build list read as no findings")
	}
}

// The baseline file must exist, because a missing one makes every caller look baselined.
func TestToolsRequiresABaseline(t *testing.T) {
	root := writeToolTree(t, toolsDockerfile, nil)
	if code := runTools(root, filepath.Join(root, "no-such-file.txt")); code == 0 {
		t.Fatal("a missing baseline passed")
	}
	_ = os.Remove(filepath.Join(root, "no-such-file.txt"))
}
