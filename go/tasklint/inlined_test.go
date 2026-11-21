package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The task fixture: a `script: |` block whose heredoc carries the runner, indented into the
// YAML scalar.
const inlinedTaskTpl = `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: end2end-run
          script: |
            #!/bin/bash
            cat > /workspace/run.sh <<'LEARTECH_RUNNER'
%s
            LEARTECH_RUNNER
`

func writeInlinedFixture(t *testing.T, indentedBody string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks/end2end"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "end2end-runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	task := fmtInlined(indentedBody)
	if err := os.WriteFile(filepath.Join(root, "tasks/end2end/pullrequest.yaml"), []byte(task), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// fmtInlined wraps an ALREADY-INDENTED body in the task.
func fmtInlined(indentedBody string) string {
	return replaceOnce(inlinedTaskTpl, "%s", indentedBody)
}

func replaceOnce(tpl, marker, body string) string {
	out := ""
	for i := 0; i < len(tpl); i++ {
		if i+len(marker) <= len(tpl) && tpl[i:i+len(marker)] == marker {
			out += body + tpl[i+len(marker):]
			return out
		}
		out += string(tpl[i])
	}
	return out
}

// Drift between the inlined runner and run.sh is the defect this check exists for: the
// pipeline executes a copy, so a fix to run.sh alone never runs.
//
// proven-by: TestInlinedRunnerMatchesRunSH
func TestInlinedRunnerMatchesRunSH(t *testing.T) {
	body := "            #!/bin/bash\n            echo hello"
	root := writeInlinedFixture(t, body)
	if err := os.WriteFile(filepath.Join(root, "end2end-runner/run.sh"), []byte("#!/bin/bash\necho hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runInlined(root); code != 0 {
		t.Fatal("a matching inlined runner was rejected")
	}

	// Change run.sh only: the copy in the task is now stale.
	if err := os.WriteFile(filepath.Join(root, "end2end-runner/run.sh"), []byte("#!/bin/bash\necho goodbye\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runInlined(root); code == 0 {
		t.Fatal("a stale inlined runner passed; the pipeline would run the old copy")
	}
}

// A heredoc whose lines are not indented into the YAML scalar ends the block early and
// leaves the task unparseable — while a content-only comparison reports it identical.
//
// proven-by: TestInlinedRejectsAnUnindentedBlock
func TestInlinedRejectsAnUnindentedBlock(t *testing.T) {
	body := "            #!/bin/bash\n#!/bin/bash is not indented here"
	root := writeInlinedFixture(t, body)
	if err := os.WriteFile(filepath.Join(root, "end2end-runner/run.sh"), []byte("#!/bin/bash\n#!/bin/bash is not indented here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runInlined(root); code == 0 {
		t.Fatal("an unindented heredoc line passed; the YAML scalar ends there")
	}
}

// No heredoc at all is a failure, not a pass: a check that finds nothing to compare and
// reports ok is the "0 checked, ok" defect.
//
// proven-by: TestInlinedRejectsAMissingHeredoc
func TestInlinedRejectsAMissingHeredoc(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks/end2end"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "end2end-runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/end2end/pullrequest.yaml"), []byte("spec: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "end2end-runner/run.sh"), []byte("#!/bin/bash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runInlined(root); code == 0 {
		t.Fatal("a task with no inlined runner passed")
	}
}
