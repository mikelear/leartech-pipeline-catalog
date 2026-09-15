// Package taskharness runs a Tekton task's embedded shell script under test.
//
// WHY THIS EXISTS. The catalog has 47 task YAMLs carrying embedded scripts,
// 43 of which run under `set -e`, and none of them had a single test. The
// largest is tasks/end2end/pullrequest.yaml at 1,281 lines -- the script that
// decides whether every repo in the estate is allowed to merge.
//
// They were "tested" by editing them and watching CI. On 2026-09-14/15 that
// cost four consecutive corrections to one 30-line block, and each correction
// was verified by a hand-built harness that differed from the real task in
// exactly the detail that mattered:
//
//	compared against the wrong VERSION   -- only wrong on a partial retest
//	substring instead of exact tag match -- only wrong at 3 vs 30
//	${img##*:} on name:tag@sha256:digest -- only wrong with a digest present
//	ran without `set -eo pipefail`       -- only wrong when a command fails
//
// Every one was invisible to a harness assembled from what the author
// expected rather than from what the task does. So this package does not let
// you retype the script, does not let you choose the shell options, and does
// not let you forget which image it runs in. It reads all three out of the
// task itself.
package taskharness

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Step is one step's script, lifted verbatim from a task YAML along with the
// image it runs in.
type Step struct {
	Task   string // path of the task YAML it came from
	Name   string // step name
	Image  string // the image the step declares
	Script string // the script, exactly as the task carries it
}

// shebang and `set` lines are read from the script rather than supplied by the
// caller: supplying them is how a harness comes to run under different options
// than the task.
var setLine = regexp.MustCompile(`(?m)^\s*set\s+-[a-zA-Z]+.*$`)

// ShellOptions returns the `set` lines the script itself declares. A test that
// wants the task's behaviour gets the task's options, not its own guess.
func (s Step) ShellOptions() []string {
	return setLine.FindAllString(s.Script, -1)
}

type taskDoc struct {
	Spec struct {
		PipelineSpec struct {
			Tasks []struct {
				TaskSpec struct {
					Steps []struct {
						Name   string `yaml:"name"`
						Image  string `yaml:"image"`
						Script string `yaml:"script"`
					} `yaml:"steps"`
				} `yaml:"taskSpec"`
			} `yaml:"tasks"`
		} `yaml:"pipelineSpec"`
	} `yaml:"spec"`
}

// LoadStep lifts one named step out of a task YAML.
//
// It fails rather than returning an empty Step when the name does not match,
// because a harness that silently tests nothing is the failure mode this
// package exists to remove -- a green test over an empty script is worse than
// no test at all.
func LoadStep(t *testing.T, taskPath, stepName string) Step {
	t.Helper()

	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("taskharness: cannot read %s: %v", taskPath, err)
	}
	var doc taskDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("taskharness: %s does not parse as a Tekton task: %v", taskPath, err)
	}

	var names []string
	for _, tk := range doc.Spec.PipelineSpec.Tasks {
		for _, st := range tk.TaskSpec.Steps {
			if st.Name == "" {
				continue
			}
			names = append(names, st.Name)
			if st.Name != stepName {
				continue
			}
			if strings.TrimSpace(st.Script) == "" {
				t.Fatalf("taskharness: step %q in %s has an empty script", stepName, taskPath)
			}
			return Step{Task: taskPath, Name: st.Name, Image: st.Image, Script: st.Script}
		}
	}
	t.Fatalf("taskharness: no step %q in %s (steps present: %s)",
		stepName, taskPath, strings.Join(names, ", "))
	return Step{}
}

// Fake is a stub binary placed ahead of the real one on PATH.
//
// Body is a shell script. Use it to make kubectl return a fixed pod list, or
// curl exit 22 the way `curl -f` does on an HTTP error -- the distinction
// between "the command failed" and "the command returned something unexpected"
// is most of what these scripts get wrong.
type Fake struct {
	Name string
	Body string
}

// ExitingFake is the common case: a stub that just exits with a code, which is
// how a failing curl or kubectl actually presents.
func ExitingFake(name string, code int) Fake {
	return Fake{Name: name, Body: fmt.Sprintf("#!/bin/sh\nexit %d\n", code)}
}

// PrintingFake is the other common case: fixed output, exit 0.
func PrintingFake(name, out string) Fake {
	return Fake{Name: name, Body: "#!/bin/sh\nprintf '%s' " + shellQuote(out) + "\n"}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Result is what the script did.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Combined is stdout and stderr together, which is what a CI log shows and
// therefore what an assertion about diagnosability should read.
func (r Result) Combined() string { return r.Stdout + r.Stderr }

// Run executes the step's script with the given fakes ahead of it on PATH and
// the given environment.
//
// The script runs EXACTLY as extracted -- no reformatting, no added or removed
// `set` lines. If the task says `set -eo pipefail`, so does the test.
func (s Step) Run(t *testing.T, env map[string]string, fakes ...Fake) Result {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("taskharness: %v", err)
	}
	for _, f := range fakes {
		p := filepath.Join(binDir, f.Name)
		if err := os.WriteFile(p, []byte(f.Body), 0o755); err != nil {
			t.Fatalf("taskharness: writing fake %s: %v", f.Name, err)
		}
	}

	scriptPath := filepath.Join(dir, "step.sh")
	if err := os.WriteFile(scriptPath, []byte(s.Script), 0o755); err != nil {
		t.Fatalf("taskharness: %v", err)
	}

	shell := "bash"
	if strings.HasPrefix(strings.TrimSpace(s.Script), "#!/bin/sh") {
		shell = "sh"
	}

	cmd := exec.Command(shell, scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("taskharness: running step %q: %v", s.Name, err)
	}
	return Result{ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String()}
}
