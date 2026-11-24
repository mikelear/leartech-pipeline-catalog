package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func shapeOf(t *testing.T, doc string) []shapeFinding {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return checkShape("fixture.yaml", &n)
}

// The defect this check exists for: a text splice doubled a key, and YAML accepted it.
//
// `resources:resources:` parses as the KEY "resources:resources" with an empty value, so
// every YAML gate in the estate passed while Tekton would reject the whole task. It shipped
// on main in tasks/helm/release.yaml and was only found by reading the rendered step.
//
// proven-by: TestShape_ReportsASplicedKey
func TestShape_ReportsASplicedKey(t *testing.T) {
	got := shapeOf(t, `
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - taskSpec:
        steps:
        - image: example:1.0
          name: s
          resources:resources:
            requests:
              cpu: 1
`)
	if len(got) != 1 {
		t.Fatalf("findings = %v, want exactly the spliced key", got)
	}
	if got[0].message[:2] != "X1" {
		t.Errorf("finding = %q, want an X1", got[0].message)
	}
}

// The control: the same document with the key written once is clean, so the check cannot be
// satisfied only by failing everything.
//
// proven-by: TestShape_AcceptsAValidTask
func TestShape_AcceptsAValidTask(t *testing.T) {
	if got := shapeOf(t, `
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        stepTemplate:
          env:
          - name: HOME
            value: /tekton/home
        steps:
        - image: example:1.0
          name: s
          resources:
            requests:
              cpu: 1
          env:
          - name: A
            value: b
          script: |
            echo hi
`); len(got) != 0 {
		t.Errorf("a valid task reported %v", got)
	}
}

// A step field Tekton does not know is a typo that the YAML parse cannot see, and Tekton
// rejects the task for it.
//
// proven-by: TestShape_ReportsAnUnknownStepField
func TestShape_ReportsAnUnknownStepField(t *testing.T) {
	got := shapeOf(t, `
spec:
  pipelineSpec:
    tasks:
    - taskSpec:
        steps:
        - image: example:1.0
          name: s
          resourses: {}
`)
	if len(got) != 1 || got[0].message[:2] != "X2" {
		t.Fatalf("findings = %v, want one X2 for the misspelt field", got)
	}
}

// Only STEP fields are constrained. A Tekton field this tool has never heard of elsewhere in
// the document must not be reported, or the check becomes a schema to maintain.
//
// proven-by: TestShape_LeavesOtherLevelsAlone
func TestShape_LeavesOtherLevelsAlone(t *testing.T) {
	if got := shapeOf(t, `
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: example:1.0
          name: s
          script: echo hi
  someFutureField: whatever
`); len(got) != 0 {
		t.Errorf("an unknown field outside a step was reported: %v", got)
	}
}
