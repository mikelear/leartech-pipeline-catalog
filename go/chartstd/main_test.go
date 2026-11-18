package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every scenario is a shape this estate has actually shipped, and the test
// asserts on the RULE that fires — not on the message, which is allowed to
// improve.
//
// The fixture that matters most is console-api-default-store: the real chart
// defaults its store to `cluster-secret-store`, which does not exist on this
// cluster, so the object reads correct and fails at sync. A chart whose store
// name is a default renders an EMPTY name when the value is not supplied, which
// is what a render can see and a template read cannot.
func TestRulesFireOnRealEstateShapes(t *testing.T) {
	cases := []struct {
		dir       string
		wantRules []string
		chart     bool
		templates bool
	}{
		{dir: "clean"},
		{dir: "console-api-default-store", wantRules: []string{"S1"}},
		{dir: "empty-store", wantRules: []string{"S1b"}},
		{dir: "double-claim", wantRules: []string{"S3"}},
		{dir: "data-and-template", wantRules: []string{"S4"}},
		{dir: "workload-identity", wantRules: []string{"S6"}},
		{dir: "todo-password", wantRules: []string{"S5"}, chart: true},
		// The console-api line, verbatim: `enabled: false` cannot disable the
		// object because sprig's `default` treats false as empty.
		{dir: "bool-default", wantRules: []string{"S8"}, chart: true, templates: true},
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.dir, "render.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			docs, err := load(tc.dir, f)
			if err != nil {
				t.Fatal(err)
			}
			findings := checkObjects(docs, wantStore)
			if tc.chart {
				findings = append(findings, checkValues(filepath.Join("testdata", tc.dir))...)
			}
			if tc.templates {
				findings = append(findings, checkTemplates(filepath.Join("testdata", tc.dir))...)
			}

			got := map[string]bool{}
			for _, f := range findings {
				got[f.Rule] = true
			}
			for _, want := range tc.wantRules {
				if !got[want] {
					t.Errorf("%s: %s did not fire; findings were %v", tc.dir, want, findings)
				}
			}
			if len(tc.wantRules) == 0 && len(findings) > 0 {
				t.Errorf("%s must be clean, got %v", tc.dir, findings)
			}
			if len(tc.wantRules) > 0 {
				for rule := range got {
					if !contains(tc.wantRules, rule) {
						t.Errorf("%s: rule %s fired but is not part of this fixture's story",
							tc.dir, rule)
					}
				}
			}
		})
	}
}

// A render that produced nothing must FAIL the tool rather than pass it. This is
// the failure mode the estate keeps re-learning: a check that runs over an empty
// set reports success while verifying nothing.
//
// proven-by: TestEmptyRenderIsAFailure
func TestEmptyRenderIsAFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte("---\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := load(path, f); err == nil {
		t.Fatal("an empty render was accepted; a check over nothing must not report success")
	} else if !strings.Contains(err.Error(), "rendered no objects") {
		t.Fatalf("wrong error for an empty render: %v", err)
	}
}

// Two charts rendered separately still collide on one Secret, so the tool has to
// judge them together. This is the console-api shape: the claimant is a DIFFERENT
// chart in the same namespace, which no single-chart check can see.
//
// proven-by: TestClaimsAcrossSeparateRendersCollide
func TestClaimsAcrossSeparateRendersCollide(t *testing.T) {
	a, err := os.Open(filepath.Join("testdata", "double-claim", "render.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	first, err := load("chart-a", a)
	if err != nil {
		t.Fatal(err)
	}
	// The same document again, stood in for "a second chart rendering the same
	// target name": one doc from each side is enough to prove the collision.
	docs := append(first, first[0])

	findings := checkObjects(docs, wantStore)
	var s3 int
	for _, f := range findings {
		if f.Rule == "S3" {
			s3++
		}
	}
	if s3 != 1 {
		t.Fatalf("want exactly one S3 finding for a shared target across renders, got %d (%v)",
			s3, findings)
	}
}

// The store every fixture is judged against: the one this estate supplies from
// config/secrets.yaml. Named here rather than in a chart so the fixtures cannot
// drift into agreeing with a wrong default.
const wantStore = "leartech-secrets"

// T1 and T2 are the behavioural half of the standard: a switch the chart CLAIMS
// has to actually remove the object. mikelear/leartech-console-api#32 is
// toggle-dead — the render with `enabled: false` still contains the ExternalSecret.
//
// proven-by: TestToggleMustRemoveTheObject
func TestToggleMustRemoveTheObject(t *testing.T) {
	cases := []struct {
		dir  string
		want string // "" means clean
	}{
		{dir: "toggle-works"},
		{dir: "toggle-dead", want: "T2"},
		{dir: "toggle-stale", want: "T1"},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			base := mustLoad(t, filepath.Join("testdata", tc.dir, "base.yaml"))
			off := mustLoad(t, filepath.Join("testdata", tc.dir, "off.yaml"))
			decl, err := loadToggles(filepath.Join("testdata", tc.dir, "toggles.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			findings := checkToggles("toggles.yaml", decl, base, [][]obj{off})
			if tc.want == "" {
				if len(findings) != 0 {
					t.Fatalf("must be clean, got %v", findings)
				}
				return
			}
			for _, f := range findings {
				if f.Rule == tc.want {
					return
				}
			}
			t.Fatalf("want %s, got %v", tc.want, findings)
		})
	}
}

func mustLoad(t *testing.T, path string) []obj {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	docs, err := load(path, f)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
