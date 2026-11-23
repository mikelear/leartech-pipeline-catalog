package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixture lays out one recorded PR: the task it replays, the env, the seeded workspace
// and the verdict it must reach.
func writeFixture(t *testing.T, root, name, task, env string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "task"), []byte(task+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if env != "" {
		if err := os.WriteFile(filepath.Join(dir, "env"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A fixture replays a SEGMENT of a shipped task and asserts the verdict of each step.
//
// proven-by: TestReplayRunsAFixtureAndChecksItsVerdict
func TestReplayRunsAFixtureAndChecksItsVerdict(t *testing.T) {
	root := t.TempDir()

	// The task under test: two steps, the second reading a file the first wrote.
	taskDir := filepath.Join(root, "tasks", "demo")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: produce
          script: |
            #!/bin/bash
            echo 'the build failed' > /workspace/.rc
            echo 1 > /workspace/.rc-value
        - name: consume
          script: |
            #!/bin/bash
            rc=$(cat /workspace/.rc-value)
            if [ "$rc" != "0" ]; then
              echo 'not building an image'
              exit 0
            fi
            echo 'went on to build'
`
	if err := os.WriteFile(filepath.Join(taskDir, "pullrequest.yaml"), []byte(task), 0o600); err != nil {
		t.Fatal(err)
	}

	fixtures := filepath.Join(root, "test", "replay")
	dir := writeFixture(t, fixtures, "ok", "tasks/demo/pullrequest.yaml", "CLUSTER_ID=gcp\n", map[string]string{
		"expect.yaml": `why: the handoff works
only: [produce, consume]
steps:
  produce:
    exit: 0
  consume:
    exit: 0
    contains: not building an image
    absent: went on to build
`,
	})

	if code := runReplay(root, fixtures); code != 0 {
		t.Fatalf("a correct replay was rejected (exit %d)", code)
	}

	// The other direction: the same fixture, expecting the branch the task does NOT take.
	if err := os.WriteFile(filepath.Join(dir, "expect.yaml"), []byte(`why: wrong on purpose
only: [produce, consume]
steps:
  consume:
    contains: went on to build
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runReplay(root, fixtures); code == 0 {
		t.Fatal("a replay whose expectation the task does not meet passed")
	}
}

// A fixture that stops before an asserted step must FAIL: a segment that stops early and
// passes is the same silence as a check that never ran.
//
// proven-by: TestReplayFailsWhenAStepIsNeverReached
func TestReplayFailsWhenAStepIsNeverReached(t *testing.T) {
	root := t.TempDir()
	taskDir := filepath.Join(root, "tasks", "demo")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - name: first
          script: |
            #!/bin/bash
            exit 1
        - name: second
          script: |
            #!/bin/bash
            echo reached
`
	if err := os.WriteFile(filepath.Join(taskDir, "pullrequest.yaml"), []byte(task), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(root, "test", "replay")
	writeFixture(t, fixtures, "stops", "tasks/demo/pullrequest.yaml", "", map[string]string{
		"expect.yaml": `why: the second step is unreachable
only: [first, second]
steps:
  first:
    exit: 1
  second:
    contains: reached
`,
	})
	if code := runReplay(root, fixtures); code == 0 {
		t.Fatal("an assertion on an unreached step passed")
	}
}

// No fixtures is a failure, not a clean run: zero replays must never read as zero regressions.
//
// proven-by: TestReplayRefusesAnEmptyFixtureSet
func TestReplayRefusesAnEmptyFixtureSet(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "test", "replay")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if code := runReplay(root, empty); code == 0 {
		t.Fatal("an empty fixture set passed")
	}
}

// The shipped fixtures must stay: each names a real defect, and a deleted fixture is a
// regression nobody is watching for any more.
func TestShippedReplayFixturesExist(t *testing.T) {
	root := filepath.Join("..", "..", "test", "replay")
	for _, name := range []string{"go-build-failed", "go-build-passed"} {
		if _, err := os.Stat(filepath.Join(root, name, "expect.yaml")); err != nil {
			t.Errorf("fixture %s is gone: %v", name, err)
		}
	}
}
