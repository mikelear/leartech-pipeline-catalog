package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The inlined end2end runner must equal run.sh, and must be well-formed YAML while it does.
//
// Inlining is deliberate: fetching the runner over raw.githubusercontent would put every
// end2end run in the estate behind GitHub's availability. The cost of that choice is drift,
// and this is what pays it — otherwise the pipeline executes a runner that passes none of
// run_test.sh.
//
// STRUCTURE IS CHECKED AS WELL AS CONTENT. Every line of a YAML literal block has to carry
// the block's indentation, INCLUDING the line the closing heredoc marker sits on. Getting
// that wrong emits a marker at column 0, ends the scalar early, and leaves the task
// unparseable — which a content-only comparison reports as identical.
//
// proven-by: TestInlinedRunnerMatchesRunSH
func runInlined(dir string) int {
	fmt.Println("=== the inlined end2end runner matches run.sh ===")

	taskPath := filepath.Join(dir, "tasks/end2end/pullrequest.yaml")
	runPath := filepath.Join(dir, "end2end-runner/run.sh")

	task, err := os.ReadFile(taskPath) // #nosec G304 -- the operator names the tree
	if err != nil {
		note("%s: %v", taskPath, err)
		return 1
	}
	want, err := os.ReadFile(runPath) // #nosec G304 -- the operator names the tree
	if err != nil {
		note("%s: %v", runPath, err)
		return 1
	}

	const openMarker = "<<'LEARTECH_RUNNER'\n"
	const closeMarker = "LEARTECH_RUNNER"
	// The literal block's indentation, which is the workflow's own: ten spaces, matching
	// `script: |` under a step in a PipelineRun.
	const indent = "            "

	body := string(task)
	i := strings.Index(body, openMarker)
	if i < 0 {
		note("no LEARTECH_RUNNER heredoc in %s. Either the step was removed — delete this "+
			"check rather than let it pass on nothing — or the marker changed and this check "+
			"is looking at the wrong thing", taskPath)
		return 1
	}
	rest := body[i+len(openMarker):]
	j := strings.Index(rest, closeMarker)
	if j < 0 {
		note("the LEARTECH_RUNNER heredoc is never closed in %s", taskPath)
		return 1
	}
	block := rest[:j]

	lines := strings.Split(block, "\n")
	for n, line := range lines {
		if line == "" || strings.HasPrefix(line, indent) {
			continue
		}
		note("line %d of the inlined block lacks the block indentation, so the task stops "+
			"being valid YAML there:\n       %q", n+1, line)
		return 1
	}
	if !strings.HasSuffix(block, "\n"+indent) {
		note("the closing LEARTECH_RUNNER marker is not indented into the block, which ends " +
			"the YAML scalar early")
		return 1
	}

	// Strip the block indentation back off, line by line, keeping the line endings so the
	// comparison is byte-for-byte against run.sh.
	var inlined strings.Builder
	for _, line := range strings.SplitAfter(block, "\n") {
		inlined.WriteString(strings.TrimPrefix(line, indent))
	}

	if got, wantStr := inlined.String(), string(want); got != wantStr {
		g, w := strings.Split(got, "\n"), strings.Split(wantStr, "\n")
		for k := 0; k < len(g) || k < len(w); k++ {
			a, b := "<missing>", "<missing>"
			if k < len(g) {
				a = g[k]
			}
			if k < len(w) {
				b = w[k]
			}
			if a != b {
				note("the inlined copy differs from end2end-runner/run.sh at line %d\n"+
					"       inlined: %q\n       run.sh:  %q\n"+
					"       Regenerate it from run.sh; do not edit the YAML by hand.", k+1, a, b)
				break
			}
		}
		return 1
	}

	fmt.Println("  ok   the inlined runner matches run.sh, and the block is well-formed YAML")
	return 0
}
