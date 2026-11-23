package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// orphan: a tool in the checkers image that nothing runs.
//
// WHY THIS EXISTS. Today's session found THREE of these by hand, and each cost an
// investigation that started from "why did nothing catch this":
//
//	argohooks      shipped in catalog#429 with tests and fixtures, wired into the image, and
//	               NOTHING INVOKED IT until #431 — a gate with no caller is a note, not a check.
//	identitystd    tests and no caller since it was written, so S9's LIVE half had never run
//	               anywhere; it took gsm#3236 to run it on a schedule.
//	scopedrift     same, and worse: when it was finally pointed at real repos it reported 15
//	               findings about two it could not read.
//	versionanchor  still has no caller: its logic lives as shell inside
//	               tasks/end2end/pullrequest.yaml and the Go tool beside it is a reference
//	               nobody runs — the two can drift and only one of them is tested.
//
// THE FAILURE THIS FIXES IS NOT "a bug slipped through". It is that the CATALOG's own test
// suite answers "does this tool work?" and never "does anything call this tool?". Every one
// of the four above passed its tests the whole time.
//
// A TOOL HAS A CALLER WHEN ANY OF THESE IS TRUE:
//
//	in a task      named as a COMMAND in a non-comment line of tasks/**/*.yaml
//	in the mk      named in go/leartech-go.mk, which every Go repo's `make lint` runs
//	in our own CI  named in .lighthouse/, i.e. this catalog's own presubmit
//	tested         the subject of a test/go/*_test.sh or a Go test — not a caller, but not
//	               orphaned either, and the distinction belongs in the output
//
// and otherwise it must be in test/go/orphan_tools.txt WITH a reason. That file is the
// point: "this tool runs nowhere" is a decision somebody wrote down, not a fact nobody
// noticed. Measured on 2026-11-23: 23 built tools, 4 orphaned, 3 of them silently.
//
// proven-by: TestOrphanFindsAToolWithNoCaller
func runOrphan(dir, baselinePath string) int {
	fmt.Println("=== a tool nothing runs is a note, not a check ===")

	tools, err := builtTools(dir)
	if err != nil {
		note("%v", err)
		return 1
	}
	baseline, err := readDelegates(baselinePath)
	if err != nil {
		// The baseline is `<tool>|<why>`; readDelegates parses exactly that shape.
		note("%v", err)
		return 1
	}
	waived := map[string]string{}
	for _, b := range baseline {
		waived[b.File] = b.Who
	}

	taskText, _ := readAllUnder(filepath.Join(dir, "tasks"), ".yaml")
	mkText, _ := readFileString(filepath.Join(dir, "go/leartech-go.mk"))
	ciText, _ := readAllUnder(filepath.Join(dir, ".lighthouse"), ".yaml")
	testText, _ := readAllUnder(filepath.Join(dir, "test/go"), ".sh")
	goTestText, _ := readAllUnder(filepath.Join(dir, "go"), "_test.go")

	var orphaned, testedOnly []string
	called := 0
	for _, tool := range tools {
		if invokesTool(taskText, tool) || namedIn(mkText, tool) || invokesTool(ciText, tool) {
			called++
			continue
		}
		if namedIn(testText, tool) || namedIn(goTestText, tool) {
			testedOnly = append(testedOnly, tool)
			continue
		}
		orphaned = append(orphaned, tool)
	}

	fmt.Printf("  %d built tool(s): %d called, %d only ever tested, %d orphaned\n",
		len(tools), called, len(testedOnly), len(orphaned))
	if len(testedOnly) > 0 {
		fmt.Println("  tested but never called in THIS tree — a passing test is not a caller:")
		for _, t := range testedOnly {
			if why, ok := waived[t]; ok {
				fmt.Printf("       %-18s (on file: %s)\n", t, why)
				continue
			}
			fmt.Printf("       %s\n", t)
		}
	}

	fail := 0
	var unwaived []string
	for _, tool := range orphaned {
		if _, ok := waived[tool]; ok {
			continue
		}
		unwaived = append(unwaived, tool)
	}
	if len(unwaived) > 0 {
		fmt.Println()
		fmt.Println("FAIL these tools run nowhere and nobody has said why:")
		for _, tool := range unwaived {
			fmt.Printf("       %s\n", tool)
		}
		fmt.Println()
		fmt.Println("Either wire it (a task step, go/leartech-go.mk, or a scheduled job in the")
		fmt.Println("cluster repo), or record the decision by adding its line to")
		fmt.Printf("  %s\n", baselinePath)
		fmt.Println("A tool with tests and no caller is how four gates sat unrun in this catalog.")
		fail++
	}

	// A baseline entry whose tool gained a caller is stale: leaving it implies the
	// decision still stands, and the next reader believes it does. A tool that is
	// TESTED-ONLY still needs its line, because "runs nowhere in THIS tree" is what the
	// line says — renovateenrolment is invoked by jx-build-cluster-gsm's own presubmit,
	// which this check cannot see.
	var stale []string
	for tool := range waived {
		if !containsString(orphaned, tool) && !containsString(testedOnly, tool) {
			stale = append(stale, tool)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Println()
		fmt.Println("FAIL these now have a caller — delete their line from the baseline:")
		for _, tool := range stale {
			fmt.Printf("       %s\n", tool)
		}
		fail++
	}

	if fail == 0 {
		fmt.Println("  ok   every built tool is called, or has a reason on file")
	}
	return boolToCode(fail == 0)
}

// builtTools is the set of binaries the checkers image ships, read from the Dockerfile's
// build list rather than from the directories — the image is what a task can call.
func builtTools(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "checkers/Dockerfile")) // #nosec G304 -- the tree
	if err != nil {
		return nil, err
	}
	// `for m in a b c; do` — the module list the build loop iterates.
	m := regexp.MustCompile(`(?m)^\s*for [bm] in ([a-z0-9 ]+); do`)
	var out []string
	for _, sm := range m.FindAllStringSubmatch(string(b), -1) {
		for _, name := range strings.Fields(sm[1]) {
			if !containsString(out, name) {
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no build list found in checkers/Dockerfile — this check would " +
			"pass over zero tools, which is the defect it exists for")
	}
	sort.Strings(out)
	return out, nil
}

// invokesTool reports whether text runs tool as a COMMAND: at the start of a line or after a
// shell separator, never as a path segment or inside a comment.
//
// This is the discrimination the neighbouring guards each learned the hard way — a checker
// that matches mentions rather than calls reports coverage it does not have. `…/authconformance/main.go`
// is a URL, `# jobreaping does X` is prose, `command -v jobreaping` is a call.
func invokesTool(text, tool string) bool {
	call := regexp.MustCompile(`(?:^|[\s;|&(])` + regexp.QuoteMeta(tool) + `(?:$|[\s;)])`)
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		// Trailing prose after a `#` is not a call.
		if i := strings.Index(t, " #"); i >= 0 {
			t = t[:i]
		}
		if call.MatchString(t) {
			return true
		}
	}
	return false
}

// namedIn is the looser test used where a mention IS the wiring: `leartech-go.mk`'s preflight
// table names a tool to decide how to fetch it, and a URL variable names the module path.
func namedIn(text, tool string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(tool) + `\b`).MatchString(text)
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the tree
	return string(b), err
}

// readAllUnder concatenates every file with the given suffix under dir. A missing directory
// is not an error: not every check applies to every tree, and the fixture case has no tasks.
func readAllUnder(dir, suffix string) (string, error) {
	var sb strings.Builder
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, suffix) {
			return nil
		}
		b, rerr := os.ReadFile(p) // #nosec G304 -- walked from dir
		if rerr == nil {
			sb.Write(b)
			sb.WriteString("\n")
		}
		return nil
	})
	return sb.String(), err
}
