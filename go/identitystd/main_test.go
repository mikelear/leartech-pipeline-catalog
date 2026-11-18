package main

import (
	"os/exec"
	"strings"
	"testing"
)

// sa-stripped is the incident verbatim: the ServiceAccount exists, the annotation
// is gone, and every other gate says green.
func TestBindingIsRequiredAndReported(t *testing.T) {
	out, code := run(t, "testdata/sa-stripped.json",
		"external-secrets=secret-infra", "argocd-repo-server=argocd")
	if code == 0 {
		t.Fatalf("a stripped annotation passed:\n%s", out)
	}
	for _, want := range []string{"secret-infra/external-secrets", "argocd/argocd-repo-server"} {
		if !strings.Contains(out, want) {
			t.Errorf("finding does not name %s:\n%s", want, out)
		}
	}
}

// A ServiceAccount that does not exist at all is a different fault with a
// different fix, and it must not be reported as a missing annotation.
//
// proven-by: TestMissingObjectIsNotAMissingAnnotation
func TestMissingObjectIsNotAMissingAnnotation(t *testing.T) {
	out, code := run(t, "testdata/sa-missing.json",
		"external-secrets=secret-infra", "argocd-repo-server=argocd")
	if code == 0 {
		t.Fatalf("a missing ServiceAccount passed:\n%s", out)
	}
	if !strings.Contains(out, "does not exist") {
		t.Errorf("a missing object should say so, got:\n%s", out)
	}
}

// proven-by: TestAPresentBindingIsQuiet
func TestAPresentBindingIsQuiet(t *testing.T) {
	out, code := run(t, "testdata/sa-good.json",
		"external-secrets=secret-infra", "argocd-repo-server=argocd")
	if code != 0 {
		t.Fatalf("a correct dump failed:\n%s", out)
	}
}

func run(t *testing.T, dump string, reqs ...string) (string, int) {
	t.Helper()
	bin := t.TempDir() + "/identitystd"
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	args := []string{"-sa", dump}
	for _, r := range reqs {
		args = append(args, "-require", r)
	}
	out, err := exec.Command(bin, args...).CombinedOutput()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatalf("run: %v", err)
		}
	}
	return string(out), code
}
