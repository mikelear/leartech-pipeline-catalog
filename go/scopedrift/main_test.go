package main

import (
	"os/exec"
	"strings"
	"testing"
)

func build(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/scopedrift"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// Each fixture isolates one outcome, and two of them are controls. The
// controls matter more than usual: the first version of this checker matched a
// scope string ANYWHERE in a Go file and reported "ok" against the real estate
// — green-ticking four scopes that auth-service's own values.yaml documents as
// gating nothing. A constant, a comment and a fixture all mention a scope;
// only a route enforces one.
func TestScopeDrift(t *testing.T) {
	bin := build(t)

	for _, tc := range []struct {
		name     string
		dir      string
		svc      string
		wantFail bool
		wantText string
	}{
		// Granted and gated through verifier.RequireScope.
		{"ok", "testdata/ok", "svc", false, "scope-drift: ok"},

		// Granted, the service IS readable, and nothing gates it. This is the
		// pre-granted class: the day a route gates it, every existing holder
		// gains the capability with no diff showing the change.
		{"pregranted", "testdata/pregranted", "svc", true, "leartechapi:ba:clients_write"},

		// Gated by a route, grantable to no client: the route answers 403 to
		// everyone, forever.
		{"unmintable", "testdata/unmintable", "svc", true, "leartechapi:ba:secret_read"},

		// An identifier resolved through its auth.Scope constant, and a bare
		// literal in the call, are both enforcement.
		{"literal", "testdata/literal", "svc", false, "scope-drift: ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command(bin, "-issuer-root", tc.dir+"/issuer", "-service-root", tc.dir+"/"+tc.svc).CombinedOutput()
			if failed := err != nil; failed != tc.wantFail {
				t.Errorf("exit failure=%v, want %v\n%s", failed, tc.wantFail, out)
			}
			if !strings.Contains(string(out), tc.wantText) {
				t.Errorf("output missing %q:\n%s", tc.wantText, out)
			}
		})
	}
}

// A SERVICE THIS CHECKER CANNOT READ IS NOT A SERVICE THAT ENFORCES NOTHING.
//
// It understands go-common's verifier.RequireScope with auth.Scope constants
// and nothing else. leartech-artifact-api and leartech-ai-gateway both gate
// routes today through their own middleware, and reporting their scopes as
// pre-granted would flag correct configuration — which is how a check gets
// switched off rather than fixed.
//
// So an unreadable service must be NAMED and its scopes SKIPPED, not counted
// against it. This is the assertion that stops the checker being worse than
// nothing as the estate converges.
func TestOpaqueServiceIsNamedNotBlamed(t *testing.T) {
	out, err := exec.Command(build(t),
		"-issuer-root", "testdata/opaque/issuer",
		"-service-root", "testdata/opaque/leartech-gateway-svc").CombinedOutput()
	if err != nil {
		t.Fatalf("an unreadable service must not fail the run: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "not assessable") {
		t.Errorf("the opaque service is not reported:\n%s", s)
	}
	if !strings.Contains(s, "leartech-gateway-svc") {
		t.Errorf("the opaque service is not NAMED, so nobody knows what to fix:\n%s", s)
	}
	if strings.Contains(s, "ENFORCED BY NOTHING") {
		t.Errorf("a scope belonging to an unreadable service was reported as "+
			"pre-granted. That flags correct configuration:\n%s", s)
	}
}

// A probe that read nothing is not a probe that found nothing.
//
// "Did it fail" is too weak an assertion here, and mutation testing showed it:
// deleting the guard's os.Exit still failed the run, because with no issuer
// every enforced scope looks unmintable. The check would then report a drift
// catastrophe whose real cause was a bad path — and the message naming that
// cause scrolls past above a list of findings.
//
// So the assertion is that it STOPS: exit 2, and no drift section at all.
func TestUnreadableIssuerStopsRatherThanReportingNonsense(t *testing.T) {
	cmd := exec.Command(build(t),
		"-issuer-root", "testdata/noissuer/issuer",
		"-service-root", "testdata/noissuer/svc")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("an issuer with no YAML passed; every conclusion would be about "+
			"an empty set:\n%s", out)
	}
	if code := cmd.ProcessState.ExitCode(); code != 2 {
		t.Errorf("exit = %d, want 2 (unreadable input), which must be distinct from "+
			"1 (real drift) so a caller can tell a bad path from a bad estate", code)
	}
	if !strings.Contains(string(out), "issuer side is unreadable") {
		t.Errorf("failure does not name the cause:\n%s", out)
	}
	for _, forbidden := range []string{"ENFORCED, GRANTABLE TO NOBODY", "ENFORCED BY NOTHING"} {
		if strings.Contains(string(out), forbidden) {
			t.Errorf("reported %q from an unreadable issuer. Every enforced scope "+
				"looks unmintable when nothing was granted, so this is a finding "+
				"about a bad path dressed as a finding about the estate:\n%s", forbidden, out)
		}
	}
}

// The failure has to say what to do. "These are pre-granted" without the
// consequence reads as pedantry and gets ignored.
func TestFailureNamesTheConsequenceAndTheFix(t *testing.T) {
	out, _ := exec.Command(build(t),
		"-issuer-root", "testdata/pregranted/issuer",
		"-service-root", "testdata/pregranted/svc").CombinedOutput()
	for _, want := range []string{
		"every existing holder",
		"No diff shows the access change",
		"Either gate them, or stop granting them",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
