package taskharness

// ASSERTIONS ABOUT A TASK'S STEPS AS A SET, which no single-script test can make.
//
// WHY THIS FILE EXISTS. The gitops-cluster task shipped five defects that its own shell script could
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
//	5. and the step written to end (4) was itself dead on arrival: `-cluster "${CLUSTER_ID:?}"` with no
//	   env supplying CLUSTER_ID, so the shell exited 1 before prpost ever ran.
//
// Each is a property of the task YAML, so each is testable here — in the catalogue's own pipeline, on
// every PR, with no cluster. That is the point of the tasks' logic having moved into Go: a rule that
// exists only as a paragraph in a comment is a rule the next edit silently un-fixes.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const gitopsTask = "../../tasks/gitops-cluster/pullrequest.yaml"

// stepDoc is the shape of one step in a Tekton task, as far as these assertions need.
type stepDoc struct {
	Name    string    `yaml:"name"`
	Image   string    `yaml:"image"`
	Script  string    `yaml:"script"`
	OnError string    `yaml:"onError"`
	Env     []stepEnv `yaml:"env"`
}

// stepEnv is one `env:` entry, reduced to the only question these tests ask: can this reference ever
// be EMPTY?
type stepEnv struct {
	Name      string  `yaml:"name"`
	Value     *string `yaml:"value"`
	ValueFrom struct {
		ConfigMapKeyRef *struct {
			Optional bool `yaml:"optional"`
		} `yaml:"configMapKeyRef"`
		SecretKeyRef *struct {
			Optional bool `yaml:"optional"`
		} `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}

// satisfiable reports whether the step is GUARANTEED to give this variable a non-empty value. A
// literal `value:` is. An `optional: true` reference is NOT: it resolves to nothing when the
// ConfigMap/Secret or the key is absent, which is exactly the state that makes `${VAR:?}` exit.
func (e stepEnv) satisfiable() bool {
	if e.Value != nil {
		return true
	}
	if r := e.ValueFrom.ConfigMapKeyRef; r != nil && !r.Optional {
		return true
	}
	if r := e.ValueFrom.SecretKeyRef; r != nil && !r.Optional {
		return true
	}
	return false
}

// loadStepsFrom lifts the steps out of ANY task YAML. loadSteps() stays as the gitops-cluster
// shortcut the older tests use.
func loadStepsFrom(t *testing.T, path string) []stepDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	var doc struct {
		Spec struct {
			PipelineSpec struct {
				Tasks []struct {
					TaskSpec struct {
						Steps []stepDoc `yaml:"steps"`
					} `yaml:"taskSpec"`
				} `yaml:"tasks"`
			} `yaml:"pipelineSpec"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// Not every file under tasks/ is a PipelineRun (some are Task YAMLs); an unparseable one is
		// not a finding here, because task_syntax/tasklint already own that.
		return nil
	}
	var out []stepDoc
	for _, tk := range doc.Spec.PipelineSpec.Tasks {
		out = append(out, tk.TaskSpec.Steps...)
	}
	return out
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

var requiredVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*):\?`)

// unsatisfiedRequiredVars returns "step: variable" for every `${VAR:?}` in a step that the step does not
// itself guarantee. Extracted from the test below so the RULE can be controlled against fixtures: a
// check that quietly finds nothing to check is a check that reports success for work it never did.
func unsatisfiedRequiredVars(steps []stepDoc) []string {
	var out []string
	for _, st := range steps {
		given := map[string]bool{}
		for _, e := range st.Env {
			given[e.Name] = given[e.Name] || e.satisfiable()
		}
		for _, m := range requiredVar.FindAllStringSubmatch(st.Script, -1) {
			if !given[m[1]] {
				out = append(out, st.Name+": "+m[1])
			}
		}
	}
	return out
}

// TestEveryTaskSuppliesTheEnvItsScriptRequires is the SAME rule, over EVERY task in the catalogue.
//
// WHY IT HAD TO WIDEN. It was written for the gitops-cluster task, whose report step used
// `-cluster "${CLUSTER_ID:?}"` with no env for it: under `set -u` that is an immediate exit 1, so the
// step died on that line BEFORE prpost ran — and every failing PR showed Lighthouse's generic "the
// following test failed" with no comment, which is exactly what the step existed to remove.
//
// Then the same grep found it in EIGHTEEN more tasks. Verified by running them: `leartech-console-api#37`
// failed with
//
//	/tekton/scripts/script-4-sxk9x: line 10: CLUSTER_ID: parameter null or not set
//
// so the report step of every one of those tasks had never run either — the failure was invisible from
// GitHub, where a red check with no comment looks the same as a check whose comment could not be posted.
//
// THE RULE, and why it is `${VAR:?}` specifically: `${VAR:-}` and `${VAR}` are a default and a value;
// `${VAR:?}` is a deliberate "abort if this is not set", so it is only correct when the step GUARANTEES
// it. That guarantee is `value:` (a literal), or a valueFrom whose key/name is NOT `optional: true` —
// an optional reference resolves to nothing when the object is absent, which is precisely the state
// `:?` rejects.
func TestEveryTaskSuppliesTheEnvItsScriptRequires(t *testing.T) {
	files, err := filepath.Glob("../../tasks/*/*.yaml")
	if err != nil || len(files) < 20 {
		t.Fatalf("found %d task files; the catalogue has far more, so this check would pass by finding "+
			"nothing", len(files))
	}
	steps, checked, bad := 0, 0, 0
	for _, f := range files {
		for _, st := range loadStepsFrom(t, f) {
			steps++
			given := map[string]bool{}
			for _, e := range st.Env {
				given[e.Name] = given[e.Name] || e.satisfiable()
			}
			for _, m := range requiredVar.FindAllStringSubmatch(st.Script, -1) {
				checked++
				if given[m[1]] {
					continue
				}
				bad++
				t.Errorf("%s: step %q requires ${%s:?} and does not guarantee it.\n"+
					"  ${VAR:?} ABORTS the shell under `set -u`, so the step dies on that line — before it\n"+
					"  can report anything — and from GitHub a red check looks the same as a check whose\n"+
					"  comment could not be posted. Supply the env (a `value:`, or a valueFrom that is not\n"+
					"  `optional: true`), or use ${%s:-} if the tool accepts empty.", f, st.Name, m[1], m[1])
			}
		}
	}
	// THE VACUITY GUARD IS ON THE STEPS, NOT ON THE PATTERN. Zero `${VAR:?}` is the DESIRED end state —
	// the estate's report steps all use `${CLUSTER_ID:-}` now — so asserting "the pattern is used" would
	// force a worse shape back in. What this test must prove is that it READ the catalogue.
	if steps < 100 {
		t.Fatalf("only %d step(s) loaded from %d task file(s): the walk did not run, and a check that "+
			"reads nothing reports success for work it never did", steps, len(files))
	}
	t.Logf("checked %d ${VAR:?} usage(s) across %d step(s) in %d task file(s), %d unsatisfied",
		checked, steps, len(files), bad)
}

// The control: the rule must FIRE on the shape that shipped in eighteen tasks. Without this, deleting
// the check would look exactly like a catalogue with no violations.
func TestTheEnvRuleFiresOnATaskWithAnUnsuppliedVar(t *testing.T) {
	dir := t.TempDir()
	task := filepath.Join(dir, "pullrequest.yaml")
	body := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: busybox
          name: report
          env:
          - name: GIT_TOKEN
            valueFrom:
              secretKeyRef:
                name: tekton-git
                key: password
                optional: true
          script: |
            #!/bin/bash
            set -uo pipefail
            prpost -cluster "${CLUSTER_ID:?}" -summary "x"
`
	if err := os.WriteFile(task, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := loadStepsFrom(t, task)
	if len(st) != 1 {
		t.Fatalf("loaded %d steps, want 1", len(st))
	}
	given := map[string]bool{}
	for _, e := range st[0].Env {
		given[e.Name] = given[e.Name] || e.satisfiable()
	}
	if given["CLUSTER_ID"] {
		t.Fatal("the fixture's GIT_TOKEN env was mistaken for CLUSTER_ID")
	}
	found := false
	for _, m := range requiredVar.FindAllStringSubmatch(st[0].Script, -1) {
		if m[1] == "CLUSTER_ID" {
			found = true
		}
	}
	if !found {
		t.Fatal("the rule did not match `${CLUSTER_ID:?}` in the shape that shipped in eighteen tasks")
	}
}

// TestRequiredEnvIsActuallyProvided is defect 4's SECOND half, and it was STILL LIVE after the PR that
// was supposed to fix 4 merged.
//
// The report step ran `prpost … -cluster "${CLUSTER_ID:?}"` and the step had NO env supplying
// CLUSTER_ID. Under `set -u`, `${VAR:?}` is not a default — it is an immediate exit 1. So the step died
// on its own line 14, BEFORE prpost, on every failing PR:
//
//	/tekton/scripts/script-3-6dgnh: line 14: CLUSTER_ID: parameter null or not set
//
// which is why a red `pr-gate` arrived with Lighthouse's generic "the following test failed" and no
// comment: the one step written to end that arrived dead. It survived review because from GitHub both
// outcomes look identical — only the pod's log separates "prpost could not post" from "prpost was never
// reached", and nobody opens the pod log of the step that exists to save them opening one.
func TestRequiredEnvIsActuallyProvided(t *testing.T) {
	if got := unsatisfiedRequiredVars(loadSteps(t)); len(got) > 0 {
		t.Errorf("%d step(s) use ${VAR:?} with no value that cannot be empty:\n  %s\n\n"+
			"  ${VAR:?} exits the shell under `set -u`, so the step dies before it can report\n"+
			"  anything. Supply the env var, or write ${VAR:-} if the tool accepts empty — prpost\n"+
			"  does: an empty cluster is deliberately 'no cluster tag'.",
			len(got), strings.Join(got, "\n  "))
	}
}

// The control for the rule above, on the two shapes that matter. Without it, deleting the check and
// having a repo with no violations look exactly alike.
func TestRequiredEnvRuleFiresOnTheShapeThatShipped(t *testing.T) {
	optionalOnly := strptr("") // a reference present but optional: cannot be trusted to be non-empty
	_ = optionalOnly

	live := []stepDoc{
		// the report step as merged: `-cluster "${CLUSTER_ID:?}"`, no env for it at all
		{Name: "report", Script: `prpost -cluster "${CLUSTER_ID:?}" -tool pr-gate`},
		// …and with an OPTIONAL reference, which is NOT a fix: the key may simply be absent
		{Name: "report", Script: `prpost -cluster "${CLUSTER_ID:?}"`, Env: []stepEnv{{Name: "CLUSTER_ID"}}},
	}
	if got := unsatisfiedRequiredVars(live); len(got) != 2 {
		t.Fatalf("the rule missed the shape that shipped: got %v, want 2 findings", got)
	}

	fixed := []stepDoc{
		// the fix that shipped: no `:?` at all
		{Name: "report", Script: `prpost -cluster "${CLUSTER_ID:-}"`},
		// …or a value the step is guaranteed to have
		{Name: "report", Script: `prpost -cluster "${CLUSTER_ID:?}"`,
			Env: []stepEnv{{Name: "CLUSTER_ID", Value: strptr("customer2")}}},
	}
	if got := unsatisfiedRequiredVars(fixed); len(got) != 0 {
		t.Errorf("the rule fires on a satisfied reference: %v — a check that cannot pass is as "+
			"useless as one that cannot fail", got)
	}
}

func strptr(s string) *string { return &s }
