package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// go test ./go/authtoken -run TestInlinedCopy -update
//
// rewrites the heredoc in the task from main.go. The drift test tells people
// to regenerate rather than hand-edit the YAML, so regenerating has to be a
// command rather than advice.
var update = flag.Bool("update", false, "rewrite the inlined copy in the end2end task from main.go")

const (
	taskPath = "../../tasks/end2end/pullrequest.yaml"
	// Indentation of the task's `script:` literal block.
	blockIndent = "            "
)

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
	block := rest[:j]

	// Structural check, independent of the content: every line of a YAML
	// literal block -- including the line the closing heredoc marker sits on
	// -- must carry the block's indentation. A line at column 0 ends the
	// scalar early and the task stops parsing, which no amount of comparing
	// Go source would notice.
	for n, line := range strings.Split(block, "\n") {
		if line == "" || strings.HasPrefix(line, blockIndent) {
			continue
		}
		t.Fatalf("line %d of the inlined block does not start with the block indentation, "+
			"so %s is not valid YAML from that point on:\n  %q", n+1, taskPath, line)
	}
	if !strings.HasSuffix(block, "\n"+blockIndent) {
		t.Fatalf("the AUTHTOKEN_GO marker in %s is not indented into the block, which ends "+
			"the YAML scalar early", taskPath)
	}

	inlined := dedent(block)

	want, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		if inlined == string(want) {
			t.Log("inlined copy already matches main.go; nothing to update")
			return
		}
		head := string(task)[:i+len(open)]
		tail := rest[j:]
		// The closing marker's own indentation lives in the block, not in
		// tail, so it has to be written back. Forgetting it emits a marker at
		// column 0, which ends the YAML block scalar early and leaves the
		// file unparseable -- done once, caught by the structural check
		// below rather than by anything downstream.
		body := indent(string(want)) + blockIndent
		if err := os.WriteFile(taskPath, []byte(head+body+tail), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote the inlined copy in %s from main.go", taskPath)
		return
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
	const indent = blockIndent
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		b.WriteString(strings.TrimPrefix(line, indent))
	}
	return b.String()
}

// indent is dedent's inverse: the YAML block indentation, with blank lines
// left blank so a round trip is byte-exact.
func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(line)
			continue
		}
		b.WriteString(blockIndent + line)
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
