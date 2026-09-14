package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Each fixture violates exactly ONE thing, and the control violates nothing.
// Without the control, a checker that failed unconditionally would pass every
// other case here — which is how two of authconformance's rules were wrong on
// first contact with the estate.
func TestJobReaping(t *testing.T) {
	bin := t.TempDir() + "/jobreaping"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	for _, tc := range []struct {
		dir      string
		wantFail bool
		wantText string
	}{
		{"testdata/bad-plain-job", true, "no ttlSecondsAfterFinished"},
		{"testdata/bad-cronjob", true, "neither successfulJobsHistoryLimit"},
		{"testdata/bad-go", true, "no TTLSecondsAfterFinished"},

		// The control. A plain Job with a TTL, a CronJob bounded by history
		// limits, and Go that sets TTLSecondsAfterFinished — three different
		// legitimate mechanisms, all of which must pass. Requiring a TTL on a
		// CronJob that already has history limits would flag a correctly
		// configured chart; 15 of the estate's 26 Job templates were compliant
		// that way when this was written.
		{"testdata/good", false, "job-reaping: ok"},

		// A scan that examined nothing is not a scan that found nothing.
		{"testdata/empty", true, "examined 0 template YAML files and 0 Go files"},
	} {
		t.Run(strings.TrimPrefix(tc.dir, "testdata/"), func(t *testing.T) {
			out, err := exec.Command(bin, "-root", tc.dir).CombinedOutput()
			failed := err != nil
			if failed != tc.wantFail {
				t.Errorf("exit failure=%v, want %v\n%s", failed, tc.wantFail, out)
			}
			if !strings.Contains(string(out), tc.wantText) {
				t.Errorf("output missing %q:\n%s", tc.wantText, out)
			}
		})
	}
}

// The message has to name the fix, not just the defect. A gate that says
// "this is wrong" without saying what to write gets worked around.
func TestFailureNamesTheFix(t *testing.T) {
	bin := t.TempDir() + "/jobreaping"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	out, _ := exec.Command(bin, "-root", "testdata/bad-go").CombinedOutput()
	for _, want := range []string{"fix:", "TTLSecondsAfterFinished", "owned by nothing"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("failure output missing %q:\n%s", want, out)
		}
	}
}
