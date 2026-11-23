package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The end2end task's phase chain, EXECUTED — not read.
//
// WHY. The gate used to be one 860-line script; it is now five named steps
// (end2end-env, -contract, -await, -run, -report) that share the pod's /workspace but NOT its
// environment, passing state through files each producer rewrites. That plumbing has its own
// failure modes and they are all silent: a step that cannot see HAS_SCRIPT runs the suite on a
// repo that declared none, and a step that cannot see SCRIPT_EXIT reports the wrong verdict.
// Neither shows up in the pipeline as anything but a red or a green.
//
// So the steps are executed here, out of the parsed YAML, against a fake workspace — the same
// text Tekton would run, not a copy of it.
//
// IT IS IN GO NOW, and the reason is the rule this whole series is about: as a shell+python
// harness it lived in end2end-runner/ and ran in the LINT step, which had to install python3
// (and jq, and bash) to run it. Here it runs in the `test` presubmit's golang image, beside the
// other tasklint checks, and `loadSteps` parses the task with a real YAML parser instead of a
// regex over fixed indentation.
//
// The matrix below is the one that mattered before, unchanged:
//
//	declared end2end/NONE      -> contract exits 0, the later phases no-op
//	neither run.sh nor NONE    -> contract exits 1
//	suite passes               -> report exits 0
//	suite fails                -> report exits 1 (and the RUN step still exits 0)
//	suite never writes results -> report exits 1 and names end2end-run
//	this commit's deploy failed-> await refuses to test the previous build
//	a required gate never opens -> await fails, and the gate's own detail reaches the comment

// phaseHarness is one temp pod: a workspace, a source checkout, and a bin dir of stubs.
type phaseHarness struct {
	t     *testing.T
	root  string
	ws    string
	src   string
	bin   string
	steps map[string]string
}

const (
	phaseEnv      = "end2end-env"
	phaseContract = "end2end-contract"
	phaseAwait    = "end2end-await"
	phaseRun      = "end2end-run"
	phaseReport   = "end2end-report"
)

var phaseNames = []string{phaseEnv, phaseContract, phaseAwait, phaseRun, phaseReport}

// fakePrpost echoes the fields it was handed on STDERR, so the harness can assert what the
// phase passed without that text polluting the body it renders. Rendering itself is pinned by
// go/prpost's golden scenarios; this harness owns the PLUMBING.
const fakePrpost = `#!/bin/bash
set -uo pipefail
outcome=""; summary=""; tool=""
while [ $# -gt 0 ]; do
  case "$1" in
    -outcome) outcome="${2:-}"; shift 2 ;;
    -summary) summary="${2:-}"; shift 2 ;;
    -tool)    tool="${2:-}"; shift 2 ;;
    -render)  render=1; shift ;;
    *) shift ;;
  esac
done
printf 'fake-prpost: tool=%s outcome=%s summary=%s\n' "${tool:-}" "${outcome:-}" "${summary:-}" >&2
if [ "${render:-0}" = "1" ]; then
  printf '<!-- leartech-%s -->\n**%s** --- %s\n` + "```" + `leartech-report\n{"tool":"%s","outcome":"%s"}\n` + "```" + `\n' \
    "${tool:-unknown}" "$outcome" "$summary" "$tool" "$outcome"
  exit 0
fi
exit 0
`

const permissiveKubectl = `#!/bin/sh
exit 0
`

const permissiveCurl = `#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in -o) shift; : > "$1";; esac; shift; done
exit 0
`

func newPhaseHarness(t *testing.T) *phaseHarness {
	t.Helper()

	// The phases parse results.json with jq — the tool the checkers image ships for them, and
	// the tool the `test` presubmit installs for THIS test. Failing here is right: a harness
	// that quietly skipped would be the "SKIP: no pyyaml" defect this whole series removed.
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("jq is not on PATH, and the end2end phases parse results.json with it. " +
			"The `test` presubmit installs it (see .lighthouse/jenkins-x/test.yaml); a local " +
			"run needs it too")
	}

	root := t.TempDir()
	h := &phaseHarness{
		t:    t,
		root: root,
		ws:   filepath.Join(root, "ws"),
		// /workspace/source IS the checkout in the pod, and the run phase writes
		// <ws>/source/<something>.sh. They have to be nested, not siblings: a harness whose
		// layout differs from the pod's can only test a layout that does not exist.
		src:   filepath.Join(root, "ws", "source"),
		bin:   filepath.Join(root, "bin"),
		steps: map[string]string{},
	}
	for _, d := range []string{h.ws, h.src, h.bin, filepath.Join(h.ws, "bin"), filepath.Join(h.src, ".jx")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// The task's own steps, from the parsed YAML, with /workspace pointed at the sandbox.
	steps, errs := loadSteps(filepath.Join("..", ".."))
	if len(errs) > 0 {
		t.Fatalf("load task steps: %v", errs[0])
	}
	for _, s := range steps {
		if s.File != "tasks/end2end/pullrequest.yaml" || s.Script == "" {
			continue
		}
		h.steps[s.Name] = strings.ReplaceAll(s.Script, "/workspace", h.ws)
	}
	var missing []string
	for _, n := range phaseNames {
		if _, ok := h.steps[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the task no longer declares the phase(s) %v; update this harness with the "+
			"split rather than deleting the assertion", missing)
	}

	h.write(filepath.Join(h.src, ".jx", "variables.sh"),
		"export APP_NAME=myapp DOMAIN=jx.leartech.com PULL_NUMBER=7 "+
			"REPO_OWNER=acme REPO_NAME=thing VERSION=1\n")
	// The env phase downloads kubectl to an absolute path; keep it in the sandbox.
	h.steps[phaseEnv] = strings.ReplaceAll(h.steps[phaseEnv], "/usr/local/bin/kubectl",
		filepath.Join(h.bin, "kubectl"))

	h.stub("apt-get", "#!/bin/sh\nexit 0\n")
	h.stub("kubectl", permissiveKubectl)
	h.stub("curl", permissiveCurl)
	h.write(filepath.Join(h.ws, "bin", "prpost"), fakePrpost)
	if err := os.Chmod(filepath.Join(h.ws, "bin", "prpost"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *phaseHarness) write(path, body string) {
	h.t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		h.t.Fatalf("write %s: %v", path, err)
	}
}

func (h *phaseHarness) stub(name, body string) {
	h.t.Helper()
	p := filepath.Join(h.bin, name)
	h.write(p, body)
	if err := os.Chmod(p, 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// fresh clears the per-route state: the contract's env file and the repo's end2end dir. Two
// routes must not share either, or the second inherits the first's HAS_SCRIPT.
func (h *phaseHarness) fresh() {
	h.t.Helper()
	_ = os.RemoveAll(filepath.Join(h.ws, ".end2end"))
	_ = os.RemoveAll(filepath.Join(h.src, "end2end"))
	if err := os.MkdirAll(filepath.Join(h.src, "end2end"), 0o755); err != nil {
		h.t.Fatal(err)
	}
}

// run executes one phase as Tekton would: the script, cwd at the source checkout, the stub bin
// dir first on PATH, and any extra env a route needs.
func (h *phaseHarness) run(name string, extraEnv ...string) (string, int) {
	h.t.Helper()
	script, ok := h.steps[name]
	if !ok {
		h.t.Fatalf("no step named %q", name)
	}
	path := filepath.Join(h.root, name+".sh")
	h.write(path, script)

	cmd := exec.Command("bash", path)
	cmd.Dir = h.src
	cmd.Env = append([]string{
		"PATH=" + h.bin + ":" + os.Getenv("PATH"),
		"HOME=" + h.root,
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			h.t.Fatalf("run %s: %v\n%s", name, err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

// fakeE2ERun installs an e2erun that writes results.json and exits with code.
func (h *phaseHarness) fakeE2ERun(results string, code int) {
	h.t.Helper()
	body := fmt.Sprintf("#!/bin/bash\ncat > \"$1/results.json\" <<'JSON'\n%s\nJSON\nexit %d\n", results, code)
	p := filepath.Join(h.ws, "bin", "e2erun")
	h.write(p, body)
	if err := os.Chmod(p, 0o755); err != nil {
		h.t.Fatal(err)
	}
}

func (h *phaseHarness) dropE2ERun() {
	h.t.Helper()
	_ = os.Remove(filepath.Join(h.ws, "bin", "e2erun"))
}

func (h *phaseHarness) suite(script string) {
	h.t.Helper()
	p := filepath.Join(h.src, "end2end", "run.sh")
	h.write(p, script)
	if err := os.Chmod(p, 0o755); err != nil {
		h.t.Fatal(err)
	}
}

func (h *phaseHarness) fileContains(path, want string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), want)
}

func (h *phaseHarness) assertCode(phase string, out string, got, want int) {
	h.t.Helper()
	if got != want {
		h.t.Errorf("%s: exit %d, want %d\n--- output ---\n%s", phase, got, want, tail(out, 8))
	}
}

func (h *phaseHarness) assertContains(phase, out, want string) {
	h.t.Helper()
	if !strings.Contains(out, want) {
		h.t.Errorf("%s: output does not contain %q\n--- output ---\n%s", phase, want, tail(out, 8))
	}
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

const passJSON = `{"success": true, "summary": "2/2 checks passed", "tests": [{"name":"a","status":"pass"},{"name":"b","status":"pass"}]}`
const failJSON = `{"success": false, "summary": "1 of 2 failed", "tests": [{"name":"a","status":"pass"},{"name":"b","status":"fail"}]}`

// A declared opt-out keeps the whole chain green: the repo has said there is nothing to test,
// and a later phase that still probes is the bug this pins.
//
// proven-by: TestPhaseChain_DeclaredNoneStaysGreen
func TestPhaseChain_DeclaredNoneStaysGreen(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.write(filepath.Join(h.src, "end2end", "NONE"), "no suite: this service is a chart library\n")

	out, code := h.run(phaseEnv)
	h.assertCode(phaseEnv, out, code, 0)
	for _, f := range []string{".end2end/env", ".end2end/lib.sh"} {
		if !h.fileContains(filepath.Join(h.ws, f), "") {
			t.Errorf("%s did not publish %s for the later phases", phaseEnv, f)
		}
	}

	out, code = h.run(phaseContract)
	h.assertCode(phaseContract, out, code, 0)
	h.assertContains(phaseContract, out, "SKIPPED: no end2end suite, declared")

	for _, name := range []string{phaseAwait, phaseRun, phaseReport} {
		out, code = h.run(name)
		h.assertCode(name, out, code, 0)
		h.assertContains(name, out, "this phase is a no-op")
	}
}

// Neither run.sh nor NONE is the "undeclared" branch, and it fails.
//
// proven-by: TestPhaseChain_UndeclaredRepoFailsTheContract
func TestPhaseChain_UndeclaredRepoFailsTheContract(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()

	out, code := h.run(phaseContract)
	h.assertCode(phaseContract, out, code, 1)
}

// A passing suite: env publishes, contract publishes HAS_SCRIPT (a later phase that cannot see
// it runs a suite on a repo that declared none), and the report says success.
//
// proven-by: TestPhaseChain_PassingSuite
func TestPhaseChain_PassingSuite(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.suite("#!/bin/bash\necho ran\n")
	h.fakeE2ERun(passJSON, 0)

	out, code := h.run(phaseEnv)
	h.assertCode(phaseEnv, out, code, 0)

	out, code = h.run(phaseContract)
	h.assertCode(phaseContract, out, code, 0)
	if !h.fileContains(filepath.Join(h.ws, ".end2end", "env.contract"), `HAS_SCRIPT="1"`) {
		t.Error("the contract phase did not publish HAS_SCRIPT for the later phases")
	}

	out, code = h.run(phaseRun)
	h.assertCode(phaseRun, out, code, 0)

	out, code = h.run(phaseReport)
	h.assertCode(phaseReport, out, code, 0)
	h.assertContains(phaseReport, out, "PASS: end2end complete")
	// The comment is rendered in Go; what this asserts is that the phase handed it the right
	// OUTCOME. The stub echoes what it was given, so a phase that always said failure shows.
	h.assertContains(phaseReport, out, "outcome=success")
}

// A failing suite: the RUN step does not decide the verdict, the REPORT step does. That split
// is the reason a suite's own exit code cannot fail the check by itself — run.sh exits 0 when
// it completed cleanly even if a script inside it failed, and results.json carries the verdict.
//
// proven-by: TestPhaseChain_FailingSuiteIsDecidedByTheReport
func TestPhaseChain_FailingSuiteIsDecidedByTheReport(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.suite("#!/bin/bash\necho ran\n")
	h.fakeE2ERun(failJSON, 0)

	h.run(phaseEnv)
	h.run(phaseContract)

	out, code := h.run(phaseRun)
	h.assertCode(phaseRun, out, code, 0)

	out, code = h.run(phaseReport)
	h.assertCode(phaseReport, out, code, 1)
	h.assertContains(phaseReport, out, "outcome=failure")
}

// A suite that dies without results.json: the report names the step whose logs explain it.
// The alternative — a verdict with no pointer — is console-api#31 in one phase.
//
// proven-by: TestPhaseChain_SuiteWithoutResultsNamesTheStepToRead
func TestPhaseChain_SuiteWithoutResultsNamesTheStepToRead(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.suite("#!/bin/bash\nexit 127\n")
	h.dropE2ERun()

	h.run(phaseEnv)
	h.run(phaseContract)

	out, code := h.run(phaseRun)
	if !h.fileContains(filepath.Join(h.ws, ".end2end", "env.run"), `SCRIPT_EXIT="127"`) {
		t.Errorf("%s did not publish the suite's exit code\n--- output ---\n%s", phaseRun, tail(out, 8))
	}

	out, code = h.run(phaseReport)
	h.assertCode(phaseReport, out, code, 1)
	h.assertContains(phaseReport, out, "end2end-run")
}

// The await phase refuses to test the PREVIOUS build when this commit's deploy failed.
//
// This is the route that was silently wrong before: the phase used to fall back to whatever pod
// was in the namespace and report its result against the new commit. On leartech-artifact-api#17
// that failed a scope gate the commit under test had already fixed. A health probe cannot tell a
// fresh pod from a stale one — both answer 200 — so the phase asks the run that deploys instead.
//
// proven-by: TestPhaseChain_AwaitRefusesAStaleBuild
func TestPhaseChain_AwaitRefusesAStaleBuild(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.suite("#!/bin/bash\necho ran\n")
	h.fakeE2ERun(passJSON, 0)

	h.run(phaseEnv)
	h.run(phaseContract)

	// A kubectl that answers the two questions this route asks: which pipelinerun deploys this
	// commit, and how did it end. Anything else is empty — the honest answer for a namespace
	// that never got a fresh deploy.
	h.stub("kubectl", `#!/bin/sh
case "$*" in
  *"get pipelineruns"*) echo "pr-run-failed" ;;
  *"get pipelinerun pr-run-failed"*) echo "Failed" ;;
  *) : ;;
esac
exit 0
`)

	out, code := h.run(phaseAwait, "PULL_PULL_SHA=deadbeefdeadbeef")
	h.assertCode(phaseAwait, out, code, 1)
	h.assertContains(phaseAwait, out, "ended as")
	h.assertContains(phaseAwait, out, "outcome=failure")
}

// A REQUIRED gate that never reports ready: leartech-auth-ui#111. Three deployments were ready,
// the version matched, and the gate held the preview closed for ten minutes on a probe URL that
// 404s — a per-repo helmfile value. The gate's own JSON is the diagnosis, so the phase has to put
// it in the comment instead of reducing it to "never returned 3 consecutive 200s".
//
// proven-by: TestPhaseChain_RequiredGateTimeoutCarriesTheGateDetail
func TestPhaseChain_RequiredGateTimeoutCarriesTheGateDetail(t *testing.T) {
	h := newPhaseHarness(t)
	h.fresh()
	h.suite("#!/bin/bash\necho ran\n")
	h.write(filepath.Join(h.src, "end2end", "preview.yaml"), `preview:
  healthPath: /health
  service: leartech-auth-ui
  gate: required
`)
	h.fakeE2ERun(passJSON, 0)

	h.run(phaseEnv)
	h.run(phaseContract)

	// curl: the gate answers 503 (present, not ready) and its detail body carries the reason.
	h.stub("curl", `#!/bin/sh
case "$*" in
  *"%{http_code}"*) printf '503'; exit 0 ;;
  *-sSf*) exit 22 ;;
  *) printf '{"ready":false,"message":"timeout after 600s: http_probe: probe returned 404"}'; exit 0 ;;
esac
`)

	// Three timeouts, all of them the phase's own: the sibling-deploy wait (120s default), the
	// overall health budget, and the gate discovery. Without the first the route spends two
	// minutes proving something that takes one second to say.
	out, code := h.run(phaseAwait,
		"END2END_SIBLING_DETECT=1", "END2END_DEPLOY_TIMEOUT=1",
		"END2END_HEALTH_TIMEOUT=1", "GATE_DETECT_TIMEOUT=1",
		"PULL_PULL_SHA=deadbeefdeadbeef")
	h.assertCode(phaseAwait, out, code, 1)
	h.assertContains(phaseAwait, out, "preview gate timeout")
	// The gate's own message has to survive into the report — it is the whole diagnosis.
	h.assertContains(phaseAwait, out, "probe returned 404")
}
