package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The regression packs.
//
// testdata/ holds real declarations: four that must pass, five that must fail for
// the reason a real PR failed. They are simultaneously
//
//   - the regression suite for the checker, so a rule cannot be weakened silently
//   - the worked examples docs/end2end-preview.md points consumers at, so the
//     documentation cannot drift from the behaviour
//
// A fixture is only added after a real finding, and its comment names the PR. That
// is the rule: if the estate has not paid for it, it is not a fixture.
func TestGoodFixturesPassTheContractCheck(t *testing.T) {
	dirs, err := filepath.Glob("testdata/*")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, d := range dirs {
		if !isDir(d) || filepath.Base(d) == "broken" {
			continue
		}
		checked++
		if code := runContract([]string{"-dir", d}); code != 0 {
			t.Errorf("%s is meant to be a passing example of the standard, and the "+
				"contract check rejected it:\n%s", d, captureContract(t, d))
		}
	}
	if checked < 4 {
		t.Errorf("only %d passing fixture(s) found; the examples are part of the standard", checked)
	}
}

// Each broken fixture must fail, and for its own reason. A checker that failed all
// of them with one message would pass a looser test and teach nobody anything, so
// the expected text is matched too.
func TestBrokenFixturesFailForTheirOwnReason(t *testing.T) {
	cases := []struct {
		dir  string
		want string
	}{
		{"testdata/broken/undeclared-health", "healthPath"},
		{"testdata/broken/wrong-probe-host", "no chart under preview/"},
		{"testdata/broken/seed-that-is-a-check", "looks like a CHECK"},
		{"testdata/broken/hardcoded-host", "pins the host"},
		{"testdata/broken/typo-env", "END2END_HEALTH_PAHT"},
	}
	for _, c := range cases {
		if !isDir(c.dir) {
			t.Fatalf("%s is missing", c.dir)
		}
		out := captureContract(t, c.dir)
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: finding does not mention %q:\n%s", c.dir, c.want, out)
		}
	}
}

// captureContract runs the check and returns its findings as text. It goes through
// allFindings, the same function the CLI prints, so a rule cannot pass here and
// skip the gate.
func captureContract(t *testing.T, dir string) string {
	t.Helper()
	return strings.Join(allFindings(dir, "web"), "\n")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
