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
	}{
		{dir: "clean"},
		{dir: "console-api-default-store", wantRules: []string{"S1b"}},
		{dir: "double-claim", wantRules: []string{"S3"}},
		{dir: "data-and-template", wantRules: []string{"S4"}},
		{dir: "workload-identity", wantRules: []string{"S6"}},
		{dir: "todo-password", wantRules: []string{"S5"}, chart: true},
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
			findings := checkObjects(docs)
			if tc.chart {
				findings = append(findings, checkValues(filepath.Join("testdata", tc.dir))...)
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

	findings := checkObjects(docs)
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

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
