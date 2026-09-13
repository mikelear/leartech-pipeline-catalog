package main

import (
	"os"
	"strings"
	"testing"
)

const taskPath = "../../tasks/end2end/pullrequest.yaml"

// TestInlinedCopyMatchesThisSource guards the one failure mode of inlining:
// the pipeline builds the copy in tasks/end2end/pullrequest.yaml, and the
// tests in this package exercise main.go. If they drift, every test here
// passes while the estate runs different code — the worst shape a test suite
// can take.
//
// Inlining is deliberate: fetching the source over raw.githubusercontent
// would put every auth suite in the estate behind GitHub's availability, and
// GitHub had a critical incident yesterday. The cost of that choice is this
// test.
func TestInlinedCopyMatchesThisSource(t *testing.T) {
	task, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", taskPath, err)
	}

	const open, close = "<<'AUTHTOKEN_GO'\n", "AUTHTOKEN_GO"
	i := strings.Index(string(task), open)
	if i < 0 {
		t.Fatalf("%s contains no AUTHTOKEN_GO heredoc. Either the step was removed — in "+
			"which case delete this test rather than let it pass on nothing — or the "+
			"marker changed and this test is no longer looking at the right thing.", taskPath)
	}
	rest := string(task)[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatal("AUTHTOKEN_GO heredoc is never closed")
	}
	inlined := dedent(rest[:j])

	want, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	if inlined != string(want) {
		t.Errorf("the copy inlined in %s differs from main.go.\n"+
			"Regenerate it from this file; do not edit the YAML by hand.\n%s",
			taskPath, firstDiff(inlined, string(want)))
	}
}

// dedent strips the YAML block indentation the heredoc carries. Blank lines
// are left alone, matching how the step was generated.
func dedent(s string) string {
	const indent = "            "
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		b.WriteString(strings.TrimPrefix(line, indent))
	}
	return b.String()
}

func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "first difference at line " + itoa(i+1) + ":\n  inlined: " + gl + "\n  main.go: " + wl
		}
	}
	return "(no line differs; check trailing bytes)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
