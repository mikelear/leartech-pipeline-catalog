package main

import (
	"strings"
	"testing"
)

// A step script that has never been valid shell. This is the dotnet-test/rust-test/
// dotnet-mutants shape: an `if … then` opened and never closed, so the step dies before
// it runs a line and the visible symptom is a missing comment, not a broken script.
//
// proven-by: TestSyntaxReportsAnUnclosedIf
func TestSyntaxReportsAnUnclosedIf(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/broken/pullrequest.yaml": taskYAML("report", `#!/bin/bash
if [ -n "$GIT_TOKEN" ]; then
  echo posting
`),
	})
	steps, errs := loadSteps(root)
	if len(errs) != 0 {
		t.Fatalf("load errors: %v", errs)
	}
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	if code := runSyntax(steps, errs); code == 0 {
		t.Fatal("runSyntax passed a script bash cannot parse")
	}
}

// The same tree, with the block closed. A check that fails everything is as useless as
// one that passes everything, so the negative case is pinned too.
func TestSyntaxPassesAValidScript(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/ok/pullrequest.yaml": taskYAML("report", `#!/bin/bash
set -uo pipefail
if [ -n "${GIT_TOKEN:-}" ]; then
  echo posting
fi
`),
	})
	steps, errs := loadSteps(root)
	if code := runSyntax(steps, errs); code != 0 {
		t.Fatal("runSyntax failed a script bash accepts")
	}
}

// A task file that is not YAML is a finding, never a skip: a check that quietly ignores
// the file it cannot read reports coverage over a smaller population.
func TestLoadStepsReportsUnparsableYAML(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/bad/pullrequest.yaml": "spec: [this is not: a mapping\n",
	})
	steps, errs := loadSteps(root)
	if len(steps) != 0 {
		t.Fatalf("want no steps, got %d", len(steps))
	}
	if len(errs) != 1 {
		t.Fatalf("want exactly one load error, got %v", errs)
	}
	if code := runSyntax(steps, errs); code == 0 {
		t.Fatal("runSyntax passed a tree it could not read")
	}
}

// isProse is the discriminator the whole ratchet depends on. A value written to a file
// (`printf 'coverage=%s\n' "$TOTAL"`) is not prose; counting it would push a step to keep
// its formatting in shell to avoid the number going up, which inverts the ratchet.
//
// proven-by: TestIsProseIgnoresAValueInterpolation
func TestIsProseIgnoresAValueInterpolation(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		// A value to a file: the counted total would be pushed up by formatting that
		// says nothing to a person, so it is data.
		{`printf 'coverage=%s\n' "$TOTAL"`, false},
		// The same value with a label around it IS a sentence.
		{`printf 'coverage: %s%%\n' "$TOTAL"`, true},
		{`echo "==> helm $HELM_VERSION"`, true},
		// Collapsed to a bare key name after stripping, so also data.
		{`printf 'cluster=%s\n' "$CLUSTER"`, false},
		// A value with no label still reads as prose to this rule, which is why a writer
		// that only needs to record one number uses a here-string or `cat` rather than
		// `echo` — that is the shape the two ratchets agree on, and it is not worth a
		// special case that would let real prose through.
		{`echo "$rc" > /workspace/.rc`, true},
	}
	for _, tc := range cases {
		if got := isProse(tc.line); got != tc.want {
			t.Errorf("isProse(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// A shell pipeline continued onto the next line starts with one pipe. A markdown table row
// has at least two. Keeping a list of shell binaries instead was the false positive that
// made `| tar -xz -C /tmp` count as prose in three files.
func TestMarkdownIgnoresAPipelineContinuation(t *testing.T) {
	if isMarkdown("| tar -xz -C /tmp && mv /tmp/helm /usr/local/bin/helm") {
		t.Error("a single-pipe continuation counted as a markdown table row")
	}
	if isMarkdown("|          help          |  something       |") == false {
		t.Error("a real table row was not counted")
	}
	if isMarkdown("### :x: Lint: **FAIL**") == false {
		t.Error("a heading was not counted")
	}
}

// Growth fails with the file named, and the message says where to lower the baseline.
//
// proven-by: TestMessageRatchetFailsOnGrowth
func TestMessageRatchetFailsOnGrowth(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/noisy/pullrequest.yaml": taskYAML("report", `#!/bin/bash
echo "### a heading a person reads"
echo "posted by whom: $WHO"
`),
	})
	baseline := writeTree(t, map[string]string{
		"baseline.txt": "0 0 tasks/noisy/pullrequest.yaml\n",
	})
	steps, errs := loadSteps(root)
	code := runMessage(steps, errs, baseline+"/baseline.txt")
	if code == 0 {
		t.Fatal("runMessage passed a task that grew the message layer")
	}
}

// A shrink is a note, not a failure: the baseline has to be lowered in the same PR, and a
// note is what tells the author to. A stale baseline that FAILED would make converting a
// task a two-PR operation every time.
func TestMessageRatchetNotesAShrink(t *testing.T) {
	root := writeTree(t, map[string]string{
		"tasks/quiet/pullrequest.yaml": taskYAML("report", "#!/bin/bash\ntrue\n"),
	})
	baseline := writeTree(t, map[string]string{
		"baseline.txt": "4 9 tasks/quiet/pullrequest.yaml\n",
	})
	steps, errs := loadSteps(root)
	if code := runMessage(steps, errs, baseline+"/baseline.txt"); code != 0 {
		t.Fatal("runMessage failed on a shrink; a shrink is what the ratchet wants")
	}
}

// A baseline that cannot be read must not read as empty, or every task looks new.
func TestParseBaselineRejectsAMalformedLine(t *testing.T) {
	baseline := writeTree(t, map[string]string{
		"baseline.txt": "not-a-count 3 tasks/x.yaml\n",
	})
	if _, err := parseBaseline(baseline + "/baseline.txt"); err == nil {
		t.Fatal("parseBaseline accepted a line whose counts are not numbers")
	} else if !strings.Contains(err.Error(), "baseline.txt") {
		t.Errorf("error does not name the file: %v", err)
	}
}
