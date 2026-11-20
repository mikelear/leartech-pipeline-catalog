package main

import "testing"

// A call site missing a required flag still posts — with that field defaulted. The comment
// appears and is quietly wrong, which is why this is a failure and not a warning.
//
// proven-by: TestPayloadFailsOnAMissingField
func TestPayloadFailsOnAMissingField(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("report", `#!/bin/bash
prpost -tool demo -check demo -outcome failure -cluster gcp -summary hi -body-file /tmp/b.md
`),
	})
	steps, errs := loadSteps(root)
	if code := runPayload(steps, errs, delegatesFixture(t), 1); code != 0 {
		t.Fatalf("a complete invocation was rejected (code %d)", code)
	}

	root = writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("report", `#!/bin/bash
prpost -tool demo -check demo -outcome failure -cluster gcp -summary hi
`),
	})
	steps, errs = loadSteps(root)
	if code := runPayload(steps, errs, delegatesFixture(t), 1); code == 0 {
		t.Fatal("a prpost call with no -body-file passed; the comment would carry no body")
	}
}

// Two post sites in one task with the same -tool overwrite each other, because the sticky
// key is tool+cluster. A re-run of the second erases the first.
func TestPayloadRejectsTwoSitesWithOneTool(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("report", `#!/bin/bash
prpost -tool demo -check demo -outcome failure -cluster gcp -summary a -body-file /tmp/a.md
prpost -tool demo -check demo -outcome success -cluster gcp -summary b -body-file /tmp/b.md
`),
	})
	steps, errs := loadSteps(root)
	if code := runPayload(steps, errs, delegatesFixture(t), 1); code == 0 {
		t.Fatal("two sites sharing one -tool passed; they overwrite one comment")
	}
}

// A bare `prpost "$FLAGS"` invocation is legal: the flags are built in a variable in the
// same script, so the builder is what gets checked.
func TestPayloadAcceptsFlagsInAVariable(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("report", `#!/bin/bash
ARGS=(-tool demo -check demo -outcome failure -summary hi -cluster gcp -body-file /tmp/b.md)
prpost "${ARGS[@]}"
`),
	})
	steps, errs := loadSteps(root)
	if code := runPayload(steps, errs, delegatesFixture(t), 1); code != 0 {
		t.Fatal("an invocation whose flags are in a variable was rejected")
	}
}

// The anchor: a matcher regression must not read as a clean catalog.
func TestPayloadAnchorCatchesAMatcherRegression(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("report", "#!/bin/bash\ntrue\n"),
	})
	steps, errs := loadSteps(root)
	if code := runPayload(steps, errs, delegatesFixture(t), 2); code == 0 {
		t.Fatal("zero call sites matched and the check still passed")
	}
}

// A task that exits non-zero and posts nothing, absent from the baseline, is the
// console-api#31 shape. proven-by: TestFailuresFlagsATaskThatExitsAndPostsNothing
func TestFailuresFlagsATaskThatExitsAndPostsNothing(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/silent/pullrequest.yaml": taskYAML("check", "#!/bin/bash\nexit 1\n"),
		"tasks/loud/pullrequest.yaml": taskYAML("report", `#!/bin/bash
prpost -tool loud -check loud -outcome failure -summary x -body-file /tmp/b.md
`),
	})
	baseline := writeTree(t, map[string]string{"baseline.txt": ""})
	if code := runFailures(root, baseline+"/baseline.txt", delegatesFixture(t), 5); code == 0 {
		t.Fatal("a task that exits 1 and posts nothing passed")
	}
}

// A `report` step with no `onError: continue` can never run: Tekton abandons the rest of
// the task the moment a step exits non-zero. It reads, from the file, exactly like one
// that works.
func TestFailuresFlagsAnUnreachableReportStep(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/trap/pullrequest.yaml": `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: check
          script: |
            #!/bin/bash
            false
        - name: report
          script: |
            #!/bin/bash
            prpost -tool trap -check trap -outcome failure -summary x -body-file /tmp/b.md
`,
	})
	baseline := writeTree(t, map[string]string{"baseline.txt": ""})
	if code := runFailures(root, baseline+"/baseline.txt", delegatesFixture(t), 5); code == 0 {
		t.Fatal("a report step that can never run passed")
	}
}

// delegatesFixture writes a minimal delegated_posters.txt. The anchor wants five entries,
// so a test that is not about the anchor supplies exactly that many real-shaped ones.
func delegatesFixture(t *testing.T) string {
	t.Helper()
	body := ""
	for _, f := range []string{
		"tasks/qa-gate/pullrequest.yaml",
		"tasks/ai-review/pullrequest.yaml",
		"tasks/security-scan/pullrequest.yaml",
		"tasks/security-scan/image-scan.yaml",
		"tasks/security-scan/dynamic/pullrequest.yaml",
	} {
		body += f + "|posts from an image\n"
	}
	root := writeTree(t, map[string]string{"delegates.txt": body})
	return root + "/delegates.txt"
}
