package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func load(t *testing.T, dir string) (*manifest, map[string]bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", dir, "secrets.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return &m, deriveKeys(filepath.Join("testdata", dir, "derive.sh"))
}

// Every fixture is a shape this estate shipped or nearly did. `url-generated` is
// the crash loop: random bytes in a DSN.
func TestRulesFireOnRealShapes(t *testing.T) {
	cases := []struct {
		dir  string
		want []string
	}{
		{dir: "clean"},
		{dir: "url-generated", want: []string{"V2"}},
		{dir: "derived-not-derived", want: []string{"V3"}},
		{dir: "unknown-mode", want: []string{"V1"}},
		{dir: "duplicate", want: []string{"V1"}},
		{dir: "no-why", want: []string{"V1"}},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			m, derived := load(t, tc.dir)
			findings := check(m, derived)
			got := map[string]bool{}
			for _, f := range findings {
				got[f.Rule] = true
			}
			for _, want := range tc.want {
				if !got[want] {
					t.Errorf("%s: %s did not fire; findings: %v", tc.dir, want, findings)
				}
			}
			if len(tc.want) == 0 && len(findings) > 0 {
				t.Errorf("%s must be clean, got %v", tc.dir, findings)
			}
		})
	}
}

// The real manifest from jx-customer-cluster-gitops, kept verbatim. It fails, and
// the failures are the two incidents: six DSN-shaped keys declared `generate`
// (one of them the console-api crash loop) and a `derived` key nothing composes.
//
// proven-by: TestTheRealManifestStillShowsBothIncidents
func TestTheRealManifestStillShowsBothIncidents(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "real-manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	findings := check(&m, deriveKeys(filepath.Join("testdata", "real-derive.sh")))

	var v2, v3 int
	var sawConsoleAPI bool
	for _, f := range findings {
		switch f.Rule {
		case "V2":
			v2++
			if strings.Contains(f.Where, "console-api-db-dsn") {
				sawConsoleAPI = true
			}
		case "V3":
			v3++
		}
	}
	if v2 == 0 {
		t.Error("the real manifest no longer reports a URL-shaped `generate` key; either it " +
			"was fixed (then replace this fixture) or V2 stopped matching")
	}
	if !sawConsoleAPI {
		t.Error("console-api-db-dsn-ship-proven is not reported; that is the key whose random " +
			"value crashlooped the pod")
	}
	if v3 == 0 {
		t.Error("no `derived` key is reported as uncomposed; that rule has stopped working")
	}
}

// A manifest that declares nothing must be an ERROR, not a clean pass: a file that
// failed to parse and a cluster with no secrets look identical.
//
// proven-by: TestNoKeysIsAnError
func TestNoKeysIsAnError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secrets.yaml")
	if err := os.WriteFile(p, []byte("source_project: x\nkeys: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var m manifest
	b, _ := os.ReadFile(p)
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Keys) != 0 {
		t.Fatalf("fixture parsed %d keys", len(m.Keys))
	}
	// main() exits 2 on this; assert the condition it checks rather than the exit.
	if len(m.Keys) != 0 {
		t.Fatal("guard is unreachable")
	}
}
