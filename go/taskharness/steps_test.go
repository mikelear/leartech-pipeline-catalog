package taskharness

// ASSERTIONS ABOUT A TASK'S STEPS AS A SET, which no single-script test can make.
//
// WHY THIS FILE EXISTS. The gitops-cluster task shipped four defects that its own shell script could
// not have caught, because every one of them is a fact about the STEPS TAKEN TOGETHER:
//
//	1. helm/helmfile were installed in ONE step and used in ANOTHER. A Tekton task's steps are separate
//	   containers that share /workspace and nothing else, so the second step had no helm at all.
//	2. the script began `#!/usr/bin/env bash` on `python:*-alpine`, which has no bash — the shebang is
//	   resolved BEFORE the first line runs, so the `apk add bash` two lines down never executed.
//	3. the gate the step runs delegates to `make check`, and the step installed no make
//	   ("make: command not found", measured: it fails 40-argo-gate group G and therefore the check).
//	4. the task failed a PR with Lighthouse's generic "the following test failed" and no cause, because
//	   nothing posted the gate's own log as the comment.
//
// Each is a property of the task YAML, so each is testable here — in the catalogue's own pipeline, on
// every PR, with no cluster. That is the point of the tasks' logic having moved into Go: a rule that
// exists only as a paragraph in a comment is a rule the next edit silently un-fixes.

import (
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const gitopsTask = "../../tasks/gitops-cluster/pullrequest.yaml"

// stepDoc is the shape of one step in a Tekton task, as far as these assertions need.
type stepDoc struct {
	Name    string `yaml:"name"`
	Image   string `yaml:"image"`
	Script  string `yaml:"script"`
	OnError string `yaml:"onError"`
}

func loadSteps(t *testing.T) []stepDoc {
	t.Helper()
	raw, err := os.ReadFile(gitopsTask)
	if err != nil {
		t.Fatalf("cannot read %s: %v", gitopsTask, err)
	}
	var doc struct {
		Spec struct {
			PipelineSpec struct {
				Params []struct {
					Name    string `yaml:"name"`
					Default string `yaml:"default"`
				} `yaml:"params"`
				Tasks []struct {
					TaskSpec struct {
						Steps []stepDoc `yaml:"steps"`
					} `yaml:"taskSpec"`
				} `yaml:"tasks"`
			} `yaml:"pipelineSpec"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s does not parse as a Tekton task: %v", gitopsTask, err)
	}
	var out []stepDoc
	for _, tk := range doc.Spec.PipelineSpec.Tasks {
		out = append(out, tk.TaskSpec.Steps...)
	}
	if len(out) < 2 {
		t.Fatalf("only %d step(s) parsed out of %s — a harness testing nothing reports success",
			len(out), gitopsTask)
	}
	return out
}

// shebang is the interpreter the kernel picks, which is decided BEFORE any line of the script runs.
func shebang(script string) string {
	trimmed := strings.TrimSpace(script)
	first := strings.SplitN(trimmed, "\n", 2)[0]
	return strings.TrimPrefix(strings.TrimSpace(first), "#!")
}

// installs reports whether the script installs the PACKAGE named tool. The name must match a whole
// field, so `make` is not satisfied by `make-js` and `git` is not satisfied by `git-lfs`.
func installs(script, tool string) bool {
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.Contains(l, "apk add") && !strings.Contains(l, "apt-get install") &&
			!strings.Contains(l, "dnf install") {
			continue
		}
		for _, f := range strings.Fields(l) {
			f = strings.Trim(f, `"'`)
			if f == tool || strings.HasPrefix(f, tool+"=") {
				return true
			}
		}
	}
	return false
}

// uses reports whether the script INVOKES the tool as a command, not as a mention in prose.
func uses(script, tool string) bool {
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		for _, f := range strings.Fields(l) {
			f = strings.Trim(f, "\"'()`")
			if f == tool {
				return true
			}
		}
	}
	return false
}

// installsViaDownload covers helm and helmfile, which are release tarballs rather than packages.
func installsViaDownload(script, tool string) bool {
	if tool != "helm" && tool != "helmfile" {
		return false
	}
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if strings.Contains(l, tool) && (strings.Contains(l, "get.helm.sh") ||
			strings.Contains(l, "/releases/download/") || strings.Contains(l, "mv /tmp/"+tool)) {
			return true
		}
	}
	return false
}

func indexOf(steps []stepDoc, name string) int {
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func findStep(t *testing.T, steps []stepDoc, name string) stepDoc {
	t.Helper()
	for _, s := range steps {
		if s.Name == name {
			return s
		}
	}
	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}
	t.Fatalf("no step %q (steps: %s)", name, strings.Join(names, ", "))
	return stepDoc{}
}

// TestShebangInterpreterExistsInTheImage is defect 2.
//
// A script that starts `#!/usr/bin/env bash` on an image without bash does not report "bash: not
// found" — the kernel returns ENOENT and Tekton reports a bare failed step. And the `apk add bash`
// that would have fixed it sits three lines further down, unexecuted, forever.
func TestShebangInterpreterExistsInTheImage(t *testing.T) {
	bashless := []string{"-alpine", "alpine:", "debian:12-slim", "gcr.io/distroless"}
	for _, s := range loadSteps(t) {
		sb := shebang(s.Script)
		if !strings.Contains(sb, "bash") {
			continue
		}
		for _, img := range bashless {
			if !strings.Contains(s.Image, img) {
				continue
			}
			t.Errorf("step %q: shebang %q but image %q has no bash. The shebang is resolved before the "+
				"script runs, so no `apk add bash` inside that script can help — use #!/bin/sh and "+
				"install bash in the same script, as tasks/gitops-cluster/pullrequest.yaml does.",
				s.Name, "#!"+sb, s.Image)
		}
	}
}

// TestStepsCannotShareAnInstalledTool is defect 1, stated as the RULE rather than the instance.
//
// Steps share /workspace and nothing else, so a tool installed by step N is absent in step N+1. The
// symptom is not always "helm: not found": the gate SKIPS a check ("no helmfile here") and reports
// PASS, which is the failure this whole task exists to prevent.
func TestStepsCannotShareAnInstalledTool(t *testing.T) {
	steps := loadSteps(t)
	tools := []string{"bash", "curl", "git", "tar", "python3", "helm", "helmfile", "make"}
	shared := map[string][]string{} // tool -> "installer -> user" pairs
	for i, s := range steps {
		for _, tool := range tools {
			if !installs(s.Script, tool) && !installsViaDownload(s.Script, tool) {
				continue
			}
			for _, later := range steps[i+1:] {
				if !uses(later.Script, tool) ||
					installs(later.Script, tool) || installsViaDownload(later.Script, tool) {
					continue
				}
				shared[tool] = append(shared[tool], s.Name+" -> "+later.Name)
			}
		}
	}
	keys := make([]string, 0, len(shared))
	for k := range shared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%q is installed in one step and used in a later one (%s). A task's steps are "+
			"separate containers sharing only /workspace: install a tool in the step that runs it.",
			k, strings.Join(shared[k], ", "))
	}
}

// TestTheGateStepCarriesEveryToolTheClusterGateRuns is defect 3, and it is the one that keeps coming
// back because the list it depends on lives in a DIFFERENT repo.
//
// `scripts/pr-gate.sh` shells out to `40-argo-gate.sh`, whose group G runs the GitOps repo's own
// `make -s check`. Measured on the alpine step before this test: "make: command not found", group G
// FAIL, the whole check red — from a task that had no idea it was a dependency.
func TestTheGateStepCarriesEveryToolTheClusterGateRuns(t *testing.T) {
	steps := loadSteps(t)
	gate := findStep(t, steps, "pr-gate")
	// THE LIST IS DERIVED FROM WHAT THE STEP RUNS, not from what is spelled in the script:
	// `bash ./scripts/pr-gate.sh` needs bash; the gate clones (git, tar), renders (helm, helmfile,
	// python3) and delegates to `make -s check`.
	for _, tool := range []string{"bash", "curl", "git", "tar", "python3", "make"} {
		if !installs(gate.Script, tool) {
			t.Errorf("step pr-gate does not install %q, but it runs scripts/pr-gate.sh -> "+
				"40-argo-gate.sh, which does. A tool missing here fails the check for a reason that "+
				"names nothing in this repo.", tool)
		}
	}
	for _, tool := range []string{"helm", "helmfile"} {
		if !installsViaDownload(gate.Script, tool) {
			t.Errorf("step pr-gate does not install %q in ITS OWN step, so the render check either "+
				"skips (and reports PASS-with-skip) or fails", tool)
		}
	}
}

// TestAFailedGateStillReportsItsOwnReason is defect 4.
//
// Lighthouse posts one sentence — "the following test failed" — and the rule is in a log nobody was
// told to open. So the step that runs the gate RECORDS its exit code without aborting the task, and a
// LATER step turns that log into the comment and carries the code. Four assertions, because all four
// are load-bearing: without onError the report never runs; without the report the cause is invisible;
// without the report's own exit code the check goes green.
func TestAFailedGateStillReportsItsOwnReason(t *testing.T) {
	steps := loadSteps(t)
	gate, report := indexOf(steps, "pr-gate"), indexOf(steps, "report")
	if gate < 0 || report < 0 {
		t.Fatalf("the task needs both a pr-gate step and a report step; found pr-gate at %d, report at %d",
			gate, report)
	}
	if report < gate {
		t.Error("the report step runs BEFORE the gate — it can only report an exit code that exists")
	}
	if steps[gate].OnError != "continue" {
		t.Errorf("step pr-gate has onError %q: without `continue` the task stops there and the report "+
			"step never runs, so the PR carries Lighthouse's generic failure and no cause",
			steps[gate].OnError)
	}
	if steps[report].OnError == "continue" {
		t.Error("step report has onError: continue — a report that cannot fail turns a red check green")
	}
	if !strings.Contains(steps[report].Script, "prpost") {
		t.Error("step report does not run prpost, so the gate's own log never reaches the PR")
	}
	if !strings.Contains(steps[report].Script, `exit "$rc"`) {
		t.Error(`step report does not exit with the gate's code — the comment would appear on a PR ` +
			`whose check is green`)
	}
}
