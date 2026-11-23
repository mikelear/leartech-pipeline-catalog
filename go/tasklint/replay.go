package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// replay: run a shipped task against a RECORDED PR, and assert the verdict it reaches.
//
// WHY. Three times this session the answer to "does this work?" came from launching the real
// thing by hand — restarting a pod to prove the GCS key was gone, running scopedrift against
// real checkouts, launching kyverno as a pod. Each was a defect that HAD shipped, and each was
// caught by a person, days later. The suite could say a tool works on its fixtures; nothing
// could say "this task, on this PR, reaches this verdict".
//
// The fixture is a directory:
//
//	test/replay/<name>/task          the task to run, repo-relative (tasks/go/pullrequest.yaml)
//	test/replay/<name>/workspace/    copied into the sandbox before the steps run (the "PR")
//	test/replay/<name>/env           KEY=VALUE lines handed to every step (Tekton's env)
//	test/replay/<name>/expect.yaml   per-step exit codes and required output
//
// and each fixture names the defect it pins, so a passing replay is a statement about
// behaviour rather than about source.
//
// WHAT IT IS NOT. It does not talk to a cluster, a forge or a registry: the steps run against
// stub binaries on PATH, exactly as the end2end phase harness runs the phase scripts. That is
// the seam that makes it cheap enough to run on every catalog PR, and it is also the limit —
// a task whose real failure needs a live cluster can be replayed only as far as its own shell.
//
// proven-by: TestReplayRunsAFixtureAndChecksItsVerdict
func runReplay(dir, fixtureRoot string) int {
	fmt.Println("=== recorded PRs, replayed against the shipped task ===")

	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		note("%s: %v (a replay with no fixtures proves nothing)", fixtureRoot, err)
		return 1
	}

	ran, fail := 0, 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		fx := filepath.Join(fixtureRoot, name)
		if _, err := os.Stat(filepath.Join(fx, "task")); err != nil {
			continue // not a fixture, e.g. a README directory
		}
		ran++
		if code := replayOne(dir, fx, name); code != 0 {
			fail++
		}
	}
	if ran == 0 {
		note("no fixtures under %s — zero replays read as zero regressions", fixtureRoot)
		return 1
	}
	if fail > 0 {
		fmt.Printf("\nFAIL %d of %d recorded PR(s) reached the wrong verdict\n", fail, ran)
		return 1
	}
	fmt.Printf("  ok   %d recorded PR(s) reach the verdict they recorded\n", ran)
	return 0
}

// replayExpect is expect.yaml: the verdict each step must reach.
type replayExpect struct {
	Why string `yaml:"why"`
	// Only replays a SEGMENT of the task: the steps named here, in task order. Some of the
	// defects worth pinning live in the HANDOFF between two steps (a build's exit code on
	// disk, read by the step after it), and replaying the toolchain that produced it would
	// make the fixture depend on a Go compiler for no extra proof.
	Only  []string                    `yaml:"only"`
	Steps map[string]replayStepExpect `yaml:"steps"`
}

type replayStepExpect struct {
	Exit     *int   `yaml:"exit"`     // nil means "not asserted"
	Contains string `yaml:"contains"` // substring the combined output must carry
	Absent   string `yaml:"absent"`   // substring the combined output must NOT carry
}

func replayOne(dir, fx, name string) int {
	fmt.Printf("-- %s\n", name)

	rawTask, err := os.ReadFile(filepath.Join(fx, "task")) // #nosec G304 -- the fixture names it
	if err != nil {
		note("%s: read task pointer: %v", name, err)
		return 1
	}
	taskPath := strings.TrimSpace(string(rawTask))

	var expect replayExpect
	if b, err := os.ReadFile(filepath.Join(fx, "expect.yaml")); err == nil { // #nosec G304
		if err := yaml.Unmarshal(b, &expect); err != nil {
			note("%s: expect.yaml: %v", name, err)
			return 1
		}
	} else {
		note("%s: expect.yaml: %v", name, err)
		return 1
	}

	sandbox := newSandbox(dir, fx, taskPath)
	defer sandbox.cleanup()

	selected := expect.Only
	if len(selected) == 0 {
		for _, st := range sandbox.steps {
			selected = append(selected, st.Name)
		}
	}

	reached := 0
	for _, stepName := range selected {
		s := sandbox.step(stepName)
		if s == nil {
			note("%s: the task no longer declares %q, so this fixture is pinned to a shape "+
				"that has moved", name, stepName)
			return 1
		}
		want, asserted := expect.Steps[s.Name]
		out, code := sandbox.run(s.Name)

		// A step with onError: continue does not stop the task; anything else does. That is
		// the property three of this catalog's defects were about, so the engine models it.
		if asserted {
			reached++
			if want.Exit != nil && code != *want.Exit {
				note("%s :: %s exited %d, want %d\n%s", name, s.Name, code, *want.Exit, tailOutput(out, 6))
				return 1
			}
			if want.Contains != "" && !strings.Contains(out, want.Contains) {
				note("%s :: %s output does not carry %q\n%s", name, s.Name, want.Contains, tailOutput(out, 6))
				return 1
			}
			if want.Absent != "" && strings.Contains(out, want.Absent) {
				note("%s :: %s output carries %q, which must not appear\n%s", name, s.Name, want.Absent, tailOutput(out, 6))
				return 1
			}
		}
		if code != 0 && s.OnError != "continue" {
			fmt.Printf("     stopped at %s (exit %d), as Tekton would\n", s.Name, code)
			break
		}
	}
	want := len(expect.Steps)
	if reached < want {
		var missing []string
		for stepName := range expect.Steps {
			if !sandbox.ran[stepName] {
				missing = append(missing, stepName)
			}
		}
		sort.Strings(missing)
		note("%s: never reached %s, so %d of %d assertions were made. A replay that stops "+
			"early and passes is the silence this exists to remove", name, strings.Join(missing, ", "),
			reached, want)
		return 1
	}
	fmt.Printf("     ok   %s\n", strings.ReplaceAll(expect.Why, "\n", " "))
	return 0
}

// sandbox is one fixture's workspace plus the steps of the task it names.
type sandbox struct {
	root    string
	ws      string
	steps   []step
	env     []string
	bin     string
	ran     map[string]bool
	cleanup func()
}

func newSandbox(dir, fx, taskPath string) *sandbox {
	root, err := os.MkdirTemp("", "replay-*")
	if err != nil {
		panic(err)
	}
	s := &sandbox{
		root:    root,
		ws:      filepath.Join(root, "workspace"),
		bin:     filepath.Join(root, "bin"),
		ran:     map[string]bool{},
		cleanup: func() { _ = os.RemoveAll(root) },
	}
	for _, d := range []string{s.ws, s.bin, filepath.Join(s.ws, "source")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			panic(err)
		}
	}

	// The "PR": workspace/ is the repo checkout (/workspace/source), which is where every task
	// starts. shared/ is the REST of /workspace — the rc files, logs and env files that steps
	// hand to each other — because a fixture that could only seed the checkout could not replay
	// a handoff, and the handoffs are where three of this catalog's defects lived.
	copyTree(filepath.Join(fx, "workspace"), filepath.Join(s.ws, "source"))
	copyTree(filepath.Join(fx, "shared"), s.ws)
	// bin/ is the fixture's stubs — a fake prpost, a helm that renders nothing. It lands on
	// PATH first, so a replay can assert what a step HANDS another tool (the argv a comment
	// carries) without having that tool.
	copyTree(filepath.Join(fx, "bin"), s.bin)

	// Tekton's env, from the fixture, with /workspace pointed at the sandbox.
	if b, err := os.ReadFile(filepath.Join(fx, "env")); err == nil { // #nosec G304
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			s.env = append(s.env, line)
		}
	}

	// The task's own steps, with /workspace rewired.
	steps, errs := loadStepsFromFiles([]string{filepath.Join(dir, taskPath)})
	if len(errs) > 0 {
		panic(errs[0])
	}
	for _, st := range steps {
		st.Script = strings.ReplaceAll(st.Script, "/workspace", s.ws)
		s.steps = append(s.steps, st)
	}
	return s
}

// run executes one step the way Tekton does: bash, cwd at the source checkout, the stub bin
// dir first on PATH.
func (s *sandbox) run(name string) (string, int) {
	path := filepath.Join(s.root, name+".sh")
	if err := os.WriteFile(path, []byte(findScript(s.steps, name)), 0o600); err != nil {
		return "", 1
	}
	_ = filepath.Walk(s.bin, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	cmd := exec.Command("bash", path)
	cmd.Dir = filepath.Join(s.ws, "source")
	cmd.Env = append([]string{"PATH=" + s.bin + ":" + os.Getenv("PATH"), "HOME=" + s.root}, s.env...)
	out, err := cmd.CombinedOutput()
	s.ran[name] = true
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		return string(out), 1
	}
	return string(out), 0
}

// step returns the named step, or nil.
func (s *sandbox) step(name string) *step {
	for i := range s.steps {
		if s.steps[i].Name == name {
			return &s.steps[i]
		}
	}
	return nil
}

func findScript(steps []step, name string) string {
	for _, s := range steps {
		if s.Name == name {
			return s.Script
		}
	}
	return ""
}

// copyTree copies src into dst, creating dst. A missing src is not an error: a fixture may
// have no workspace of its own.
func copyTree(src, dst string) {
	_ = filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if b, rerr := os.ReadFile(p); rerr == nil { // #nosec G304 -- walked from src
			return os.WriteFile(target, b, info.Mode())
		}
		return nil
	})
}

// tailOutput is the last n lines, for an error that has to show the reason without showing
// the whole log.
func tailOutput(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
