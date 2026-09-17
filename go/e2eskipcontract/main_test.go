package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The four historical shapes this checker exists to separate. Each case is a
// real configuration seen in the estate on 2026-09-17, not an invention.
func TestExitCodes(t *testing.T) {
	const honouringRunner = `#!/usr/bin/env bash
SKIP_EXIT=77
rc_script=0
bash "$script" >"$log" 2>&1 || rc_script=$?
if [ "$rc_script" -eq 0 ]; then status="pass"
elif [ "$rc_script" -eq "$SKIP_EXIT" ]; then status="skip"
else status="fail"; fi
`
	// leartech-auth-service's runner before #213: no concept of 77.
	const deafRunner = `#!/usr/bin/env bash
if bash "$script" >"$log" 2>&1; then status="pass"; else status="fail"; fi
`
	const skipScript = "#!/usr/bin/env bash\necho \"SKIP: not here\"\nexit 77\n"
	const plainScript = "#!/usr/bin/env bash\necho ok\nexit 0\n"

	cases := []struct {
		name    string
		files   map[string]string
		want    int
		wantOut string
	}{
		{
			name:    "scripts exit 77 and the runner is deaf to it",
			files:   map[string]string{"01-a.sh": skipScript, "run.sh": deafRunner},
			want:    1,
			wantOut: "does not treat 77 specially",
		},
		{
			name:  "scripts exit 77 and the runner honours it",
			files: map[string]string{"01-a.sh": skipScript, "run.sh": honouringRunner},
			want:  0,
		},
		{
			name:  "no script exits 77, so a deaf runner harms nothing yet",
			files: map[string]string{"01-a.sh": plainScript, "run.sh": deafRunner},
			want:  0,
		},
		{
			name:  "no runner at all: staging dispatches no pack here",
			files: map[string]string{"01-a.sh": skipScript},
			want:  0,
		},
		{
			// REGRESSION PIN. The first version of this checker exited 2 here,
			// which would have failed the next PR in leartech-helm-library,
			// leartech-infra-agent, leartech-orchestrator-controller and
			// leartech-sc-event-listener — four repos whose end2end/ holds no
			// numbered script because their suite is shaped differently.
			// Nothing here exits 77, so there is no contract to violate.
			name:  "a suite with no script exiting 77 is not a failure",
			files: map[string]string{"run.sh": deafRunner, "helper.sh": plainScript},
			want:  0,
		},
		{
			// The blindness the old guard was worried about, handled properly:
			// a script that does not follow the numbering convention still
			// counts, because the walk reads every .sh.
			name:  "an unnumbered script exiting 77 is still caught",
			files: map[string]string{"run.sh": deafRunner, "smoke-check.sh": skipScript},
			want:  1,
		},
	}

	bin := buildOnce(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for n, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			out, code := run(t, bin, "-dir", dir)
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
}

// A missing directory is not a failure — most repos have no e2e suite — but it
// must say so rather than exiting silently.
func TestMissingDirectoryIsAnnouncedNotSilent(t *testing.T) {
	bin := buildOnce(t)
	out, code := run(t, bin, "-dir", filepath.Join(t.TempDir(), "nope"))
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !contains(out, "nothing to check") {
		t.Fatalf("a missing directory produced no explanation:\n%s", out)
	}
}

func TestEmptyDirFlagIsACallerError(t *testing.T) {
	bin := buildOnce(t)
	if _, code := run(t, bin, "-dir", ""); code != 64 {
		t.Fatalf("exit %d, want 64 for an empty -dir", code)
	}
}

// The narrow regex matters more than the broad one: a runner reported as safe
// when it is not is the failure this checker was written to prevent.
func TestRunnerDetectionDoesNotMatchAnIncidental77(t *testing.T) {
	bin := buildOnce(t)
	dir := t.TempDir()
	// 77 appears, but only as a timeout. This runner cannot handle a skip.
	os.WriteFile(filepath.Join(dir, "run.sh"),
		[]byte("#!/usr/bin/env bash\nTIMEOUT=77\ncurl --max-time 77 x\nif bash \"$s\"; then :; fi\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "01-a.sh"),
		[]byte("#!/usr/bin/env bash\nexit 77\n"), 0o755)
	out, code := run(t, bin, "-dir", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1 — a bare 77 in an unrelated position was read as skip handling:\n%s", code, out)
	}
}

func buildOnce(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "e2eskipcontract")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return string(out), code
}

func contains(h, n string) bool {
	return len(h) >= len(n) && (func() bool {
		for i := 0; i+len(n) <= len(h); i++ {
			if h[i:i+len(n)] == n {
				return true
			}
		}
		return false
	})()
}

// TestTheShapesThisEstateActuallyHas runs the checker against runner bodies
// copied VERBATIM from the repos, not paraphrases of them.
//
// A hand-written fixture proves the regex matches what I imagined a runner
// looks like. These prove it matches what the estate has. The two differ:
// auth-service's deaf runner uses `if bash ... ; then`, with no rc variable
// and no numeric comparison anywhere, which is precisely why nothing noticed
// it could not express a skip.
func TestTheShapesThisEstateActuallyHas(t *testing.T) {
	// leartech-auth-service/end2end/run.sh at b56fb66 — the commit merged as
	// #209, which set Arrival.phase=Failed for 0.1.130 and 0.1.131 on both
	// clusters and turned leartech-gate red on every GitOps PR.
	const authServiceBeforeFix = `
    if bash "$script" >"$log" 2>&1; then
      status="pass"; message="OK"
      passed=$((passed + 1))
    else
      status="fail"
      message=$(tail -3 "$log" 2>/dev/null | tr '\n' ' ' | head -c 300)
      [ -z "$message" ] && message="(no output)"
      failed=$((failed + 1))
    fi
`
	// The same file after #213.
	const authServiceAfterFix = `
SKIP_EXIT=77
    rc_script=0
    bash "$script" >"$log" 2>&1 || rc_script=$?
    if [ "$rc_script" -eq 0 ]; then
      status="pass"; message="OK"
      passed=$((passed + 1))
    elif [ "$rc_script" -eq "$SKIP_EXIT" ]; then
      status="skip"
      skipped=$((skipped + 1))
    else
      status="fail"
      failed=$((failed + 1))
    fi
`
	// leartech-maestro-service/end2end/run.sh — the repo that had the
	// convention right the whole time, and the one the FAIL message points at.
	const maestro = `
  SKIP_EXIT=77
      rc_script=0
      bash "$script" >"$log" 2>&1 || rc_script=$?
      if [ "$rc_script" -eq 0 ]; then
        status="pass"; message="OK"
      elif [ "$rc_script" -eq "$SKIP_EXIT" ]; then
        status="skip"
      else
        status="fail"
      fi
`
	// The catalog's own inlined runner form, which compares the literal.
	const literalCompare = `
      if [ "$rc_script" -eq 77 ]; then status="skip"; fi
`
	cases := []struct {
		name   string
		runner string
		want   int
	}{
		{"auth-service before #213 — broke staging", authServiceBeforeFix, 1},
		{"auth-service after #213", authServiceAfterFix, 0},
		{"maestro-service, correct throughout", maestro, 0},
		{"a runner comparing the literal 77", literalCompare, 0},
	}

	bin := buildOnce(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, "run.sh", tc.runner)
			// One script that signals a skip the new way. Without this the
			// mismatch cannot arise and every case would pass.
			mustWrite(t, dir, "01-preview-only.sh", "#!/usr/bin/env bash\necho \"SKIP: preview only\"\nexit 77\n")
			out, code := run(t, bin, "-dir", dir)
			if code != tc.want {
				t.Fatalf("exit %d, want %d\n%s", code, tc.want, out)
			}
		})
	}
}

// The skip vocabulary auth-service and maestro actually use, so a change to
// the script-side regex has to keep recognising real files.
func TestSkipSignalsSeenInRealScripts(t *testing.T) {
	real := map[string]struct {
		body string
		is77 bool
	}{
		// auth-service/end2end/01-smoke.sh after #209
		"01-smoke.sh": {"#!/usr/bin/env bash\nif [ -n \"${STAGING_URL:-}\" ]; then\n  echo \"[smoke] preview-only checks — skipping in staging\"\n  exit 77\nfi\n", true},
		// auth-service/end2end/18-scope-enforcement.sh after #208
		"18-scope.sh": {"#!/usr/bin/env bash\nif [ \"$MODE\" != \"preview\" ]; then\n  echo \"SKIP: the scope fixture is a preview-only deployment\"\n  exit 77\nfi\n", true},
		// maestro/end2end/02-messaging.sh — exits 77 with no echo on that line
		"02-messaging.sh": {"#!/usr/bin/env bash\nif [ -z \"$PREVIEW_NAMESPACE\" ]; then\n  echo \"SKIP: no PREVIEW_NAMESPACE.\"\n  exit 77\nfi\n", true},
		// a script that genuinely passes
		"03-plain.sh": {"#!/usr/bin/env bash\necho ok\n", false},
	}
	for name, tc := range real {
		if got := scriptSkips.MatchString(tc.body); got != tc.is77 {
			t.Errorf("%s: scriptSkips matched %v, want %v", name, got, tc.is77)
		}
	}
}

func mustWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
