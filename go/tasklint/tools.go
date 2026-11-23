package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// tools: a step that calls a CATALOG binary must run where that binary exists.
//
// WHY. catalog#443. `tasks/chart-standard`'s render step ran in `jx-boot` — helm yes, chartstd no —
// and called `chartstd -list-toggles`. So on any repo that DECLARED toggles the step died at
// "chartstd: command not found", and the FAIL line beside it named the toggles FILE, so a task
// defect read as a repo defect. A repo declaring none passed, because of an `if [ -f "$decl" ]`
// guard. The behavioural half of the chart gate had never run anywhere.
//
// NOTHING CAUGHT IT, and the guards that should have are the reason this check exists:
//
//	chartstd_runs (renders)  asserts a rendering task MENTIONS chartstd. A mention says nothing
//	                         about which image the step runs in — which is exactly the gap.
//	phase_tools (phases)     owns "the phase runs in an image with its tools", and its population
//	                         is two hand-named files: tasks/end2end and tasks/end2end-ui.
//	the replay corpus        stubs the tool, so the image is never consulted.
//
// THE RULE, and it is deliberately narrow enough to be knowable from this repo:
//
//	A CATALOG binary is one the checkers image builds (its Dockerfile's build list). The catalog
//	knows what is in that image and cannot know what is in anybody else's — jx-boot, golang, node.
//	So a step either RUNS in the checkers image, or is STAGED from one in the same task (the
//	`provide-catalog-tools` pattern: copy /usr/local/bin/prpost to /workspace/bin), or its line
//	goes in test/go/tool_callers.txt with the reason.
//
// Measured on the estate when this landed: one step calls a catalog binary outside the checkers
// image and stays, with a reason — end2end's `authtoken-build`, which BUILDS authtoken from the
// embedded Go source rather than calling the image's copy.
//
// proven-by: TestToolsFindsACatalogBinaryCalledFromTheWrongImage
func runTools(dir, baselinePath string) int {
	fmt.Println("=== a step that calls a catalog binary runs where that binary is ===")

	catalog, err := catalogBinaries(dir)
	if err != nil {
		note("%v", err)
		return 1
	}
	waived, err := readDelegates(baselinePath)
	if err != nil {
		note("%v", err)
		return 1
	}
	reason := map[string]string{}
	for _, w := range waived {
		reason[w.File] = w.Who
	}

	steps, loadErrs := loadSteps(dir)
	if len(loadErrs) > 0 {
		reportLoadErrors(loadErrs)
		return 1
	}

	// Every binary a checkers step stages for later steps: `cp /usr/local/bin/X …`,
	// `test -x /workspace/bin/X`, or the provide-* step naming it.
	staged := map[string]map[string]bool{} // task file -> binary
	for _, s := range steps {
		if !isCheckersImage(s.Image) {
			continue
		}
		for _, b := range catalog {
			if stagesBinary(s.Script, b) {
				if staged[s.File] == nil {
					staged[s.File] = map[string]bool{}
				}
				staged[s.File][b] = true
			}
		}
	}

	fail := 0
	checked := 0
	var findings, examined []string
	for _, s := range steps {
		if isCheckersImage(s.Image) {
			continue // the image has them all, by its own build assertion
		}
		body := stripComments(s.Script)
		for _, b := range catalog {
			if !callsBinary(body, b) {
				continue
			}
			checked++
			key := s.File + "::" + s.Name + "::" + b
			if staged[s.File][b] {
				examined = append(examined, fmt.Sprintf("%s :: %s -> %s (staged by a checkers step)", s.File, stepName(s), b))
				continue
			}
			if _, ok := reason[key]; ok {
				examined = append(examined, fmt.Sprintf("%s :: %s -> %s (on file: %s)", s.File, stepName(s), b, reason[key]))
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"%s :: step %s runs in %q and calls %q, which only the checkers image has. "+
					"Either move the step to ghcr.io/mikelear/leartech-checkers, stage the binary "+
					"from a checkers step in this task, or record it in %s",
				s.File, stepName(s), shortImage(s.Image), b, baselinePath))
			fail++
		}
	}

	fmt.Printf("  %d call(s) of a catalog binary from a step outside the checkers image\n", checked)
	for _, line := range examined {
		fmt.Printf("       %s\n", line)
	}
	fmt.Println()
	if len(findings) > 0 {
		fmt.Println("FAIL these steps call a binary their image does not have:")
		for _, f := range findings {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
		fmt.Println("A step in the wrong image fails at `command not found`, and the check around it")
		fmt.Println("blames whatever it was checking — measured on leartech-console-api#37, where a")
		fmt.Println("task defect read as a repo defect for weeks.")
	}

	// A stale baseline line is a decision that no longer applies.
	var stale []string
	for key := range reason {
		parts := strings.SplitN(key, "::", 3)
		if len(parts) != 3 {
			stale = append(stale, key)
			continue
		}
		still := false
		for _, s := range steps {
			if s.File != parts[0] || s.Name != parts[1] {
				continue
			}
			if isCheckersImage(s.Image) || staged[s.File][parts[2]] ||
				!callsBinary(stripComments(s.Script), parts[2]) {
				continue
			}
			still = true
		}
		if !still {
			stale = append(stale, key)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		fmt.Println()
		fmt.Println("FAIL these no longer apply — delete their line from the baseline:")
		for _, k := range stale {
			fmt.Printf("       %s\n", k)
		}
		fail++
	}

	if fail == 0 {
		fmt.Println("  ok   every catalog binary is called from an image that has it, or staged")
	}
	return boolToCode(fail == 0)
}

// catalogBinaries is the set of binaries the checkers image builds — the tools this repo OWNS
// and can therefore reason about. `helm` is in the image too, but it is upstream software whose
// presence in jx-boot this repo cannot verify, so it is deliberately not in this set.
func catalogBinaries(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "checkers/Dockerfile")) // #nosec G304 -- the tree
	if err != nil {
		return nil, err
	}
	// BOTH loops match: the build list (`for m in`) and the verify list (`for b in`) name every
	// tool, and counting a binary twice doubles every finding it appears in.
	m := regexp.MustCompile(`(?m)^\s*for [bm] in ([a-z0-9 ]+); do`)
	seen := map[string]bool{}
	var out []string
	for _, sm := range m.FindAllStringSubmatch(string(b), -1) {
		for _, name := range strings.Fields(sm[1]) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no build list in checkers/Dockerfile — this check would pass " +
			"over zero binaries, which is the defect it exists for")
	}
	sort.Strings(out)
	return out, nil
}

func isCheckersImage(img string) bool { return strings.Contains(img, "leartech-checkers") }

// shortImage is the image name for a finding: the tag matters less than the repo.
func shortImage(img string) string {
	if i := strings.Index(img, "@"); i > 0 {
		img = img[:i]
	}
	return img
}

// callsBinary reports whether a step RUNS the binary: at the start of a command, never as a path
// segment and never inside a comment (the caller strips those first).
func callsBinary(body, bin string) bool {
	re := regexp.MustCompile(`(?:^|[\s;|&(])` + regexp.QuoteMeta(bin) + `(?:$|[\s;)])`)
	return re.MatchString(body)
}

// stagesBinary reports whether this step copies the binary somewhere a later step can run it.
func stagesBinary(script, bin string) bool {
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(t, "/"+bin) && (strings.HasPrefix(t, "cp ") || strings.Contains(t, "cp /usr/local/bin")) {
			return true
		}
	}
	return false
}

// stripComments drops whole-line comments, which are prose about a tool rather than a call to it.
// Every guard in this catalog has had to learn this one.
func stripComments(script string) string {
	var out []string
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		if i := strings.Index(t, " #"); i >= 0 {
			t = t[:i]
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}
