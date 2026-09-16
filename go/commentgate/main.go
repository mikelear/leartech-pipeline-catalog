// commentgate challenges prose. It does not ban it.
//
// A comment cannot fail. That is the whole problem: this estate has repeatedly
// acted on comments that were wrong, while the tests beside them were right.
// Today alone: artifact-api's storage-disabled branch was commented "the 503
// stub never actually runs because BearerAuth above 401s every request first",
// which is false for any authenticated caller and hid a missing scope gate;
// auth-service's README described a manual setup step the chart had automated
// months earlier; and a design document asserted Hydra returns a token without
// a disallowed audience when it in fact returns 400.
//
// TWO RULES, both applied to CHANGED LINES ONLY.
//
//  1. RATCHET. A change may not add more non-functional comment lines than it
//     adds test lines. Narration is not free: if something needs explaining,
//     the explanation competes with proving it.
//
//  2. CLAIMS. An added comment that asserts behaviour — must, never, always,
//     ensures, guarantees, cannot, prevents — must name the test that proves
//     it:
//
//     // proven-by: TestPreviewPostureRoutesAreScopeGated
//
//     and the gate verifies that test exists. A renamed or deleted test then
//     breaks the build, so the claim cannot outlive its proof.
//
// Functional directives are exempt: //go:build, //nolint, // Code generated,
// shebangs, # renovate:, SPDX. They instruct a tool rather than asserting
// anything, so they cannot be wrong in the way prose is.
//
// Existing comments in main are untouched. A ratchet that demanded an
// 8,000-line cleanup before anything could merge would stall every repo, and
// the gap closes as files are touched anyway.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	// Comment openers per language. Keyed loosely: the gate cares that a line
	// is prose, not which dialect it is.
	commentRe = regexp.MustCompile(`^\s*(//|#|--|\{\{/\*|\*)`)

	// Instructs a tool. Cannot be a false claim about behaviour.
	functionalRe = regexp.MustCompile(`(?i)go:build|\+build|nolint|Code generated|^#!|renovate:|SPDX|yamllint|checkov:|gosec:|eslint-|prettier-|swagger:|@Success|@Failure|@Param|@Router|@Summary|@Description|@Tags|@Accept|@Produce|@Security|\+goose|proven-by:`)

	// Asserts behaviour. These are the words that made prose authoritative.
	claimRe = regexp.MustCompile(`(?i)\b(must|never|always|ensures?|guarantees?|cannot|can't|prevents?|is safe|no longer|impossible)\b`)

	provenByRe = regexp.MustCompile(`proven-by:\s*([A-Za-z0-9_./-]+)`)

	// _test.sh and _test.bats are counted, and /tests?/ matches a DIRECTORY
	// segment rather than only a path ending in one.
	//
	// The `$` anchors the whole alternation, so `/tests?/` could only ever
	// match a path ending in "/tests/" -- which a file path never does. The
	// effect was that shell tests counted as zero test lines unless they sat
	// under end2end/, so a repo adding scripts/foo_test.sh saw its ratchet
	// fail with "0 test lines" against a real test file, and the fix that
	// looks obvious is to misfile the test to satisfy the regex.
	//
	// Hit for real: leartech-ba-service's first-acquisition download script
	// is shell, because the script IS the artifact a new user runs, and its
	// 150-line test suite was invisible here.
	testFileRe = regexp.MustCompile(`(_test\.go|_test\.py|_test\.sh|_test\.bats|\.spec\.ts|\.test\.ts|(^|/)tests?/|end2end/.*\.sh|_test\.rs|Tests?\.cs)`)

	// Files that are pure pattern lists. Their comments label groups of
	// globs; there is no behaviour to prove, so counting them means a
	// hygiene change can never explain itself.
	//
	// The rule below fails when prose exceeds test lines, so with zero tests
	// ANY comment fails -- and a .gitignore commit legitimately has no tests.
	// leartech-go-service-template #149 and leartech-maestro-service #13 both
	// stalled on five lines naming which artifacts were being ignored and why.
	//
	// Deliberately narrow: three filenames, not a category. The gate exists to
	// stop narration standing in for tests in code and charts, and widening
	// that exemption is how it stops meaning anything.
	patternListRe = regexp.MustCompile(`(^|/)\.(gitignore|dockerignore|gitattributes)$`)

	// Declaration files that a checker VERIFIES against the code. Their
	// comments explain a declared value, and the value is cross-checked —
	// .authprofile says what kind of auth participant a service is, and
	// authconformance fails the build on a contradiction with what the code
	// does. That is a stronger binding than a unit test: the declaration
	// cannot drift from the behaviour without failing.
	//
	// Without this, a .authprofile can carry no explanation at all, because
	// the ratio rule fails on any prose with zero test lines and a
	// declaration file has none. leartech-gate #23 and leartech-plan-api #38
	// both stalled on exactly that, and the alternative was a bare
	// "type: inbound-resource-server" with nothing saying how it was
	// determined — which is what the file's own convention argues against.
	//
	// Narrow on purpose, and for a specific reason rather than a category:
	// the file is itself verified. A .yaml or .go comment is not.
	verifiedDeclarationRe = regexp.MustCompile(`(^|/)\.(authprofile|authconformance)$`)

	// A claim inside a TEST file needs no proven-by: the file is the proof.
	// Documentation-through-tests is the goal, so a test header stating what it
	// proves is the shape we want, not the shape we are policing.
	//
	// And a claim inside quotation marks is being REPORTED, not asserted —
	// typically the wrong comment a change is removing. Flagging that would
	// punish recording the history.
	// A comment marker followed by three or more spaces is a block quote of
	// output — an error message or log line being recorded. The claim words in
	// it belong to the tool that printed them, not to the author.
	blockQuoteRe = regexp.MustCompile(`^\s*(//|#|--)\s{3,}\S`)

	quotedRe = regexp.MustCompile(`["'"'"'“”].*\b(must|never|always|ensures?|guarantees?|cannot|can't|prevents?)\b.*["'"'"'“”]`)
)

type finding struct {
	file, line, text, kind string
}

func main() {
	base := flag.String("base", "origin/main", "base ref to diff against")
	flag.Parse()

	added, err := addedLines(*base)
	if err != nil {
		// A gate that cannot see the diff must say so, not pass. Reporting
		// "no findings" when the diff is unreadable is the silent-zero defect
		// this estate keeps hitting.
		fmt.Fprintf(os.Stderr, "commentgate: cannot read the diff against %s: %v\n", *base, err)
		os.Exit(2)
	}

	rep := evaluate(added, testExists)
	comments, testLines, claims, dangling := rep.comments, rep.testLines, rep.claims, rep.dangling

	fmt.Printf("==> commentgate: +%d prose comment line(s), +%d test line(s) against %s\n", comments, testLines, *base)

	fail := false

	if comments > testLines {
		fail = true
		fmt.Printf("\nFAIL: this change adds %d prose comment line(s) and %d test line(s).\n", comments, testLines)
		fmt.Println("A comment cannot fail, so it cannot be relied on. If the behaviour is worth")
		fmt.Println("explaining, it is worth proving: add the test, or delete the narration.")
		fmt.Println("Functional directives (go:build, nolint, generated markers, swagger annotations)")
		fmt.Println("are exempt and not counted.")
	}

	if len(claims) > 0 {
		fail = true
		fmt.Printf("\nFAIL: %d added comment(s) assert behaviour without naming a proof:\n\n", len(claims))
		for _, c := range claims {
			fmt.Printf("  %s:%s\n    %s\n", c.file, c.line, c.text)
		}
		fmt.Println("\nEither delete the claim, or name the test that proves it:")
		fmt.Println("    // proven-by: TestThatProvesIt")
		fmt.Println("The gate checks that test exists, so the claim cannot outlive its proof.")
	}

	if len(dangling) > 0 {
		fail = true
		fmt.Printf("\nFAIL: %d proven-by reference(s) name something that does not exist:\n\n", len(dangling))
		for _, d := range dangling {
			fmt.Printf("  %s:%s -> %s\n", d.file, d.line, d.text)
		}
	}

	if fail {
		os.Exit(1)
	}
	fmt.Println("==> commentgate: ok")
}

type report struct {
	comments, testLines int
	claims, dangling    []finding
}

// evaluate applies both rules to the added lines. Pure, so the rules can be
// tested without a git repository — exists is injected for the same reason.
func evaluate(added []addedLine, exists func(string) bool) report {
	var r report
	for _, a := range added {
		if testFileRe.MatchString(a.file) {
			r.testLines++
		}
		if !commentRe.MatchString(a.text) || functionalRe.MatchString(a.text) {
			continue
		}
		if patternListRe.MatchString(a.file) {
			continue // a label for a group of globs, not a claim about behaviour
		}
		if verifiedDeclarationRe.MatchString(a.file) {
			continue // a declaration a checker cross-checks against the code
		}
		if testFileRe.MatchString(a.file) {
			continue // sits with its proof; this is the documentation we want
		}
		r.comments++
		if quotedRe.MatchString(a.text) || blockQuoteRe.MatchString(a.text) {
			continue // reporting a claim, not making one
		}
		if claimRe.MatchString(a.text) && !provenByRe.MatchString(a.text) {
			r.claims = append(r.claims, finding{a.file, a.line, strings.TrimSpace(a.text), "claim"})
		}
	}
	for _, a := range added {
		m := provenByRe.FindStringSubmatch(a.text)
		if m == nil {
			continue
		}
		if !exists(m[1]) {
			r.dangling = append(r.dangling, finding{a.file, a.line, m[1], "dangling"})
		}
	}
	return r
}

type addedLine struct{ file, line, text string }

// addedLines returns lines introduced by this change, with their file and
// line number, from `git diff --unified=0`.
func addedLines(base string) ([]addedLine, error) {
	out, err := exec.Command("git", "diff", "--unified=0", "--no-color", base+"...HEAD").Output()
	if err != nil {
		// Fall back to a two-dot diff: shallow clones have no merge base.
		out, err = exec.Command("git", "diff", "--unified=0", "--no-color", base).Output()
		if err != nil {
			return nil, err
		}
	}
	return parseDiff(string(out))
}

// parseDiff is separate from the git call so it can be tested against a
// fixture. A gate whose own parsing is untested would report "no findings" on
// a diff it failed to read, which is the defect it exists to catch.
func parseDiff(out string) ([]addedLine, error) {
	var res []addedLine
	var file string
	lineNo := 0
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	hunkRe := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)`)
	for sc.Scan() {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "+++ b/"):
			file = strings.TrimPrefix(l, "+++ b/")
		case strings.HasPrefix(l, "@@"):
			if m := hunkRe.FindStringSubmatch(l); m != nil {
				fmt.Sscanf(m[1], "%d", &lineNo)
			}
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			res = append(res, addedLine{file, fmt.Sprint(lineNo), strings.TrimPrefix(l, "+")})
			lineNo++
		}
	}
	return res, sc.Err()
}

// testExists looks for a Go test function of that name, or a file of that name
// (so an end2end script can be named as the proof).
func testExists(name string) bool {
	if out, err := exec.Command("git", "grep", "-l", "func "+name).Output(); err == nil && len(out) > 0 {
		return true
	}
	if out, err := exec.Command("git", "ls-files", "*"+name).Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}
	return false
}
