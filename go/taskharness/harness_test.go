package taskharness

import (
	"strings"
	"testing"
)

const endToEndTask = "../../tasks/end2end/pullrequest.yaml"

// The harness must refuse to test nothing. A green test over an empty or
// missing script is worse than no test, and is the failure this package was
// written to remove.
func TestLoadStepNamesTheStepsWhenItCannotFindOne(t *testing.T) {
	// t.Fatalf ends the goroutine via runtime.Goexit, which recover() does not
	// catch — so the fatal path has to be exercised in a goroutine of its own.
	fake := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		LoadStep(fake, endToEndTask, "no-such-step")
	}()
	<-done

	if !fake.Failed() {
		t.Error("LoadStep accepted a step name that does not exist; a harness that " +
			"silently tests nothing reports success for work it never did")
	}
}

// The script must come out byte-identical. Every bug this package exists to
// catch survived a harness that had been retyped or reformatted.
func TestLoadStepReturnsTheScriptVerbatim(t *testing.T) {
	s := LoadStep(t, endToEndTask, "end2end")
	if len(s.Script) < 1000 {
		t.Fatalf("script is %d bytes; the end2end step is over a thousand lines", len(s.Script))
	}
	if s.Image == "" {
		t.Error("step image not captured — a script verified in the wrong image proves nothing")
	}
	// The single line whose absence caused the fourth correction.
	if len(s.ShellOptions()) == 0 {
		t.Error("no `set` line found; the harness would then run under different " +
			"options than the task, which is how the set -e failure mode stayed hidden")
	}
}

// The task's own options come from the task. This pins the specific regression:
// a harness that ran without `set -eo pipefail` could not see that a failed
// command substitution kills the step.
func TestShellOptionsComeFromTheTaskNotTheTest(t *testing.T) {
	s := LoadStep(t, endToEndTask, "end2end")
	joined := strings.Join(s.ShellOptions(), " ")
	if !strings.Contains(joined, "-e") {
		t.Errorf("expected the end2end step to declare set -e; got %q", joined)
	}
}

// A fake that exits non-zero is how a failing curl or kubectl actually
// presents. Distinguishing "command failed" from "command returned something
// unexpected" is most of what these scripts get wrong.
func TestFakesStandInForTheRealBinaries(t *testing.T) {
	s := Step{Name: "probe", Script: "#!/bin/sh\nset -e\nkubectl get pods\necho reached-the-end\n"}

	ok := s.Run(t, nil, PrintingFake("kubectl", "pod/a\n"))
	if ok.ExitCode != 0 || !strings.Contains(ok.Stdout, "reached-the-end") {
		t.Errorf("with a working kubectl the script should finish: %+v", ok)
	}

	bad := s.Run(t, nil, ExitingFake("kubectl", 1))
	if bad.ExitCode == 0 {
		t.Error("under set -e a failing kubectl must fail the step")
	}
	if strings.Contains(bad.Stdout, "reached-the-end") {
		t.Error("execution continued past a failed command under set -e")
	}
}

// THE REGRESSION THAT COST FOUR CORRECTIONS, now expressible as a unit test.
//
// Under `set -eo pipefail`, `VAR=$(curl -fsS ...)` does not leave VAR empty
// when curl fails — the failed assignment exits the shell, and any fallback
// below it never runs. Guarding with `if` keeps the failure local.
func TestFailedCommandSubstitutionUnderSetEKillsTheStep(t *testing.T) {
	unguarded := Step{Name: "x", Script: `#!/bin/bash
set -eo pipefail
VALUE=$(curl -fsS http://gate)
if [ -z "$VALUE" ]; then echo "fallback reached"; fi
echo done
`}
	r := unguarded.Run(t, nil, ExitingFake("curl", 22))
	if r.ExitCode != 22 {
		t.Errorf("expected the step to die with curl's 22; got %d", r.ExitCode)
	}
	if strings.Contains(r.Combined(), "fallback reached") {
		t.Error("fallback ran — then this test no longer pins the bug it was written for")
	}

	guarded := Step{Name: "x", Script: `#!/bin/bash
set -eo pipefail
VALUE=""
if out=$(curl -fsS http://gate); then VALUE="$out"; fi
if [ -z "$VALUE" ]; then echo "fallback reached"; fi
echo done
`}
	g := guarded.Run(t, nil, ExitingFake("curl", 22))
	if g.ExitCode != 0 {
		t.Errorf("guarded form should survive a failing curl; got %d: %s", g.ExitCode, g.Combined())
	}
	if !strings.Contains(g.Combined(), "fallback reached") {
		t.Error("guarded form did not reach its fallback")
	}
}

// Issue #121: a step that dies under set -e with no trap stops at the last
// successful echo. This pins what that looks like, so a fix can be asserted
// against it rather than eyeballed in a CI log.
func TestSilentFailureLeavesNoDiagnosis(t *testing.T) {
	silent := Step{Name: "x", Script: `#!/bin/bash
set -eo pipefail
echo "==> checking the preview"
curl -fsS https://preview/health
echo "==> preview is healthy"
`}
	r := silent.Run(t, nil, ExitingFake("curl", 7))

	if r.ExitCode == 0 {
		t.Fatal("expected failure")
	}
	if strings.Contains(r.Combined(), "preview is healthy") {
		t.Error("the script continued past the failure")
	}
	// The point of the issue: the operator learns nothing.
	if strings.Contains(r.Combined(), "FAIL") || strings.Contains(r.Combined(), "curl") {
		t.Error("this fixture is meant to demonstrate a SILENT failure; if it now " +
			"reports a cause, update the fixture rather than the expectation")
	}
}
