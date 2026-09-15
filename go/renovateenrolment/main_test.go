package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Each fixture violates exactly ONE thing, and two controls violate nothing.
// The controls matter more than usual here: a check that demanded enrolment of
// every chart would flag bitnami/postgresql, and a check that trusted any
// exemption line would honour a blank one. Both are easy to write and wrong.
func TestRenovateEnrolment(t *testing.T) {
	bin := build(t)

	for _, tc := range []struct {
		dir      string
		wantFail bool
		wantText string
	}{
		// The defect this exists for: deployed, and Renovate was never told.
		{"testdata/missing-enrolment", true, "absent from Renovate's repositories list"},

		// An exemption with no stated reason is indistinguishable from an
		// omission, which is the thing being checked for.
		{"testdata/exempt-no-reason", true, "no reason given"},

		// Controls.
		{"testdata/good", false, "renovate-enrolment: ok"},
		{"testdata/exempt-with-reason", false, "renovate-enrolment: ok"},
		{"testdata/upstream-chart", false, "renovate-enrolment: ok"},

		// Neither input being readable must fail loudly. A scan that examined
		// nothing is not a scan that found nothing.
		{"testdata/empty", true, "Nothing to compare against"},
		{"testdata/no-repositories", true, "parsed to zero entries"},
	} {
		t.Run(strings.TrimPrefix(tc.dir, "testdata/"), func(t *testing.T) {
			out, err := exec.Command(bin, "-gitops-root", tc.dir).CombinedOutput()
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

// The control passes for the RIGHT reason. Without this, `good` would also pass
// if the walker silently read no helmfiles, if the chart regex matched nothing,
// or if every release were filtered out as "not ours" — three ways to be green
// while checking nothing. The counts are the evidence that the comparison
// actually had two sides.
func TestControlPassesBecauseItSawBothSides(t *testing.T) {
	out, err := exec.Command(build(t), "-gitops-root", "testdata/good").CombinedOutput()
	if err != nil {
		t.Fatalf("control fixture must pass: %v\n%s", err, out)
	}
	for _, want := range []string{
		"2 deployed release(s) of ours",
		"2 enrolled",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("control passed without evidence it saw %q:\n%s", want, out)
		}
	}
}

// upstream-chart must pass because the upstream and in-repo charts were
// filtered out, NOT because it read nothing. It deploys four releases — one
// dev/ chart of ours, two third-party aliases and one ../../charts path — and
// exactly one must be judged ours.
func TestUpstreamChartsAreExcludedNotUnread(t *testing.T) {
	out, err := exec.Command(build(t), "-gitops-root", "testdata/upstream-chart").CombinedOutput()
	if err != nil {
		t.Fatalf("fixture must pass: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "1 deployed release(s) of ours") {
		t.Errorf("expected exactly 1 of 4 releases judged ours:\n%s", out)
	}
}

// The message has to name the fix, not just the defect. A gate that says "this
// is wrong" without saying what to write gets worked around.
func TestFailureNamesTheFix(t *testing.T) {
	out, _ := exec.Command(build(t), "-gitops-root", "testdata/missing-enrolment").CombinedOutput()
	for _, want := range []string{
		"fix:",
		`add "mikelear/leartech-plan-api" to the repositories list`,
		"or exempt it with a reason",
		"no CVE alerts",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("failure output missing %q:\n%s", want, out)
		}
	}
}

func build(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/renovateenrolment"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}
