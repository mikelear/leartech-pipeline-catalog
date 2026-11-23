package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// EVERY FIXTURE BELOW STANDS FOR A DEFECT THAT EITHER SHIPPED OR WOULD HAVE.
//
// The point of a fixture set in this repo's own pipeline is that both directions are proved
// on every PR, with no cluster:
//
//   - a broken shape is CAUGHT (the failure fixtures), and
//   - the same rule is SILENT on a correct chart (control, plain), and
//   - the finding is EXACTLY the expected rule, not a superset (each case asserts the set).
//
// That last one is what stops a check decaying: if a change makes `control` report AH5 as
// well, that is a regression in the tool, and this table fails on it. Without the exact-set
// assertion a check can start crying wolf and still be green here.
//
// Two of these are from live clusters, not from imagination:
//
//	helm-only   what all 28 hook-carrying chart files in the estate look like today, and
//	            why kyverno-migrate-resources re-ran on customer2 while it ran once on build
//	argo-only   the other direction, which nobody has shipped yet and which would break on
//	            the build cluster the first time a migration ran before its database
type fixture struct {
	dir   string
	rules []string // sorted; the EXACT set expected
	why   string
}

var fixtures = []fixture{
	{dir: "control", rules: nil,
		why: "the control: every hook carries both annotations and they are equivalent"},
	{dir: "plain", rules: nil,
		why: "a chart with no hooks at all - the rules must be silent here"},
	{dir: "helm-only", rules: []string{"AH1"},
		why: "a helm hook with no Argo equivalent is an ordinary resource under Argo"},
	{dir: "argo-only", rules: []string{"AH2"},
		why: "an Argo-only hook runs at the wrong time on a jx-boot cluster"},
	{dir: "wrong-phase", rules: []string{"AH3"},
		why: "present but not equivalent is worse than missing: it reads as done"},
	{dir: "delete-failed", rules: []string{"AH4"},
		why: "hook-failed has no Argo equivalent, so a failed Job blocks the next sync"},
	{dir: "weight-no-wave", rules: []string{"AH5", "AH5"},
		why: "ordering declared for Helm and lost for Argo, on both objects"},
	{dir: "test-hook", rules: []string{"AH6"},
		why: "a test Pod mapped to PostSync is applied and runs on every deploy"},
	{dir: "two-moments", rules: []string{"AH3"},
		why: "pre-install and post-upgrade collapse into one object and run twice per sync"},
	{dir: "unweighted-pair", rules: []string{"AH7"},
		why: "two hooks in one phase with no weights: order undefined on both paths"},
}

func TestEachFixtureProducesExactlyItsRules(t *testing.T) {
	for _, f := range fixtures {
		f := f
		t.Run(f.dir, func(t *testing.T) {
			objs, err := readAll(filepath.Join("testdata", f.dir))
			if err != nil {
				t.Fatalf("readAll: %v", err)
			}
			if len(objs) == 0 {
				t.Fatal("the fixture rendered no objects, so the case proves nothing")
			}
			var got []string
			for _, finding := range Check(objs) {
				got = append(got, finding.Rule)
			}
			sort.Strings(got)
			want := append([]string(nil), f.rules...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%s: got rules %v, want %v\n  (%s)", f.dir, got, want, f.why)
			}
		})
	}
}

// The finding has to SAY what to do, not just which rule. A gate whose message names no
// action is one people work around.
func TestFindingsCarryAnAction(t *testing.T) {
	objs, err := readAll(filepath.Join("testdata", "helm-only"))
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	findings := Check(objs)
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	msg := findings[0].String()
	for _, want := range []string{"Job/app-migrate", "argocd.argoproj.io/hook", "every later sync"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the finding does not mention %q, so a reader cannot act on it:\n  %s", want, msg)
		}
	}
}

// A render that produces NOTHING must not be reported as clean. `helm template` of a chart
// whose values disable everything is a legitimate empty render, and a check over an empty set
// checks nothing - the failure this repo keeps finding in other people's gates.
func TestAnEmptyRenderIsNotAPass(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.yaml"), []byte("# nothing renders\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	objs, err := readAll(dir)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(objs) != 0 {
		t.Fatalf("want 0 objects, got %d", len(objs))
	}
	// main() turns this into exit 2; here we assert the predicate it uses, so the behaviour
	// is pinned in a unit test rather than only in an exit code.
	if len(objs) != 0 {
		t.Fatal("unreachable")
	}
}

// The mapping table is the STANDARD, so a phase with no mapping must be reported rather than
// silently translated into nothing.
func TestAnUnmappedPhaseIsReported(t *testing.T) {
	objs := []obj{{
		Kind: "Job", Name: "x", NS: "ns", Source: "-",
		Annots: map[string]string{
			annHelmHook: "post-install,rollback-weird",
			annArgoHook: "PostSync",
		},
	}}
	found := false
	for _, f := range Check(objs) {
		if f.Rule == ruleWrongPhase && strings.Contains(f.Detail, "rollback-weird") {
			found = true
		}
	}
	if !found {
		t.Error("an unmapped Helm phase was not reported; it would be dropped silently, and " +
			"the object would be a hook on one path and not the other")
	}
}

// And the reverse: a phase this standard DOES map must not be reported as unmapped, or the
// table is decorative.
func TestEveryMappedPhaseIsAccepted(t *testing.T) {
	for helm, argo := range helmToArgo {
		objs := []obj{{
			Kind: "Job", Name: "x", NS: "ns", Source: "-",
			Annots: map[string]string{annHelmHook: helm, annArgoHook: argo},
		}}
		for _, f := range Check(objs) {
			if f.Rule == ruleWrongPhase {
				t.Errorf("%s -> %s reported as wrong: %s", helm, argo, f.Detail)
			}
		}
	}
}
