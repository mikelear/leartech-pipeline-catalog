package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// The dual-payload contract: every comment this catalog posts carries BOTH halves.
//
// Each message is two payloads in one comment: markdown for a human, and a fenced
// ```leartech-report block for an agent. That is what lets a reader see the verdict and a
// machine act on it, and it is why `prpost` exists at all.
//
// It is also easy to break in a way nothing notices: a `prpost` call missing `-outcome`
// still posts (defaulting to success), missing `-check` still posts (against the wrong
// context), missing `-body-file` still posts (with only the summary as the body). None of
// those fail the step. The comment appears; it is just quietly wrong — the same class as
// every other defect in this catalog.
var (
	// P1: bare `prpost` AND `/workspace/bin/prpost`. The tail must START with a flag, or
	// with a variable that expands to flags — an INVOCATION, not a mention. Matching
	// mentions reported 36 "sites" once, which is the same failure this exists to prevent.
	prpostCall = regexp.MustCompile(`(?:^|\s)(?:/workspace/bin/)?prpost\s+(-|"?\$\{)`)

	prpostRequired = []string{"-tool", "-check", "-outcome", "-summary", "-cluster", "-body-file"}

	toolFlag  = regexp.MustCompile(`-tool\s+(\S+)`)
	checkFlag = regexp.MustCompile(`-check\s+(\S+)`)
)

// payloadAliases records where a task posts under a tool name that is NOT its own, on
// purpose. Each entry claims the tool name is chosen for the sticky marker, not the task
// name. Shrink-only: an entry that no longer differs is a stale line.
var payloadAliases = map[string]string{
	// go/pullrequest posts under -tool build (the marker, kept stable so an existing
	// comment is updated rather than orphaned) while -check names the CONTEXT a reader
	// looks for: "pr". triggersync found the mismatch.
	"go":             "build",
	"dotnet-mutants": "mutants",
	"dotnet-test":    "coverage",
	"go-test":        "coverage",
	"ng-test":        "coverage",
	"rust-test":      "coverage",
}

// payloadAnchor is P1's own anchor: the invocation count is known, so a drop means the
// matcher regressed rather than the catalog getting cleaner. Without it a regex bug
// reports "0 checked, ok".
//
// 27 invocations in 27 files, counted 2026-11-20. The first version of this anchor was
// wrong twice — once by counting mentions (36), once by being a hand-count (9) — which is
// why it is a floor and not an equality.
const defaultPayloadAnchor = 20

// proven-by: TestPayloadFailsOnAMissingField
func runPayload(steps []step, loadErrs []error, delegatesPath string, anchor int) int {
	fmt.Println("=== every posted comment carries a human half and a machine half ===")
	reportLoadErrors(loadErrs)

	posted, bad := 0, len(loadErrs)
	byFile := map[string]int{}
	toolsByFile := map[string][]string{}

	for _, s := range steps {
		for _, m := range prpostCall.FindAllStringSubmatchIndex(s.Script, -1) {
			// The byte offset of the flag-or-variable this match ends at.
			flagged := s.Script[m[2]:m[3]] == "-"
			raw := s.Script[m[3]-1:]
			// A multi-line invocation is one command: join the continuations, THEN take
			// the first line. Splitting first truncated flags onto lines the check never
			// saw, which read as "missing -summary" on a correct call.
			tail := strings.Split(strings.ReplaceAll(raw, "\\\n", " "), "\n")[0]

			posted++
			byFile[s.File]++

			var missing []string
			if flagged {
				for _, f := range prpostRequired {
					if !strings.Contains(tail, f) {
						missing = append(missing, f)
					}
				}
			} else {
				// Flags are built into a variable in this script; the builder is the
				// thing to check, so the whole script is searched.
				for _, f := range []string{"-tool", "-check", "-outcome"} {
					if !strings.Contains(s.Script, f) {
						missing = append(missing, f)
					}
				}
			}
			if len(missing) > 0 {
				bad++
				note("%s :: step %s", s.File, stepName(s))
				fmt.Printf("       missing %s\n", strings.Join(missing, ", "))
				fmt.Println("       the comment would still post, with a defaulted field — " +
					"a payload that looks complete and is not")
				continue
			}

			tool := firstGroup(toolFlag, tail)
			check := firstGroup(checkFlag, tail)
			if tool != "" {
				toolsByFile[s.File] = append(toolsByFile[s.File], tool)
			}
			if tool != "" && check != "" && tool != check {
				dir := taskDir(s.File)
				if payloadAliases[dir] != tool {
					bad++
					note("%s :: -tool %s but -check %s, and that difference is not in the alias table",
						s.File, tool, check)
					fmt.Println("       -tool names the sticky marker, -check names the check; " +
						"an unrecorded difference means one of them is wrong")
				}
			}
		}
	}

	for _, file := range sortedKeys(toolsByFile) {
		tools := toolsByFile[file]
		seen := map[string]int{}
		var dupes []string
		for _, t := range tools {
			seen[t]++
		}
		for t, n := range seen {
			if n > 1 {
				dupes = append(dupes, t)
			}
		}
		if len(dupes) > 0 {
			sort.Strings(dupes)
			bad++
			note("%s :: posts more than once with -tool %s", file, strings.Join(dupes, ", "))
			fmt.Println("       the sticky key is tool+cluster, so those sites overwrite one comment")
		}
	}

	fmt.Printf("  %d prpost invocation(s) checked in %d file(s)\n", posted, len(byFile))

	// The population this check can read. A task that posts from INSIDE an image has no
	// prpost call, so it is invisible here — and named out loud, because a guard that
	// reports green over a population it quietly shrank is the defect it exists to stop.
	reportDelegates(delegatesPath)

	if posted < anchor {
		bad++
		note("only %d call site(s) matched, expected at least %d", posted, anchor)
		fmt.Println("       A dropped match reads as a passing check over fewer sites. Fix the")
		fmt.Println("       matcher, or lower the anchor in the same PR as a real removal.")
	}

	if bad == 0 {
		fmt.Println("  ok   every post site supplies both halves")
	}
	return boolToCode(bad == 0)
}

func stepName(s step) string {
	if s.Name == "" {
		return "(unnamed)"
	}
	return s.Name
}

// taskDir is the task's directory name, which is what the alias table is keyed on:
// tasks/go/pullrequest.yaml -> "go".
func taskDir(file string) string {
	parts := strings.Split(file, "/")
	if len(parts) > 1 {
		return parts[1]
	}
	return ""
}

func firstGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

// reportDelegates names the posting paths built inside an image. Deleting an entry does
// not fix a payload; it moves the finding back into the silent category, which is why the
// caller anchors their count too.
func reportDelegates(path string) {
	delegates, err := readDelegates(path)
	if err != nil {
		note("%v", err)
		return
	}
	if len(delegates) == 0 {
		return
	}
	fmt.Printf("  %d posting path(s) are OUTSIDE this check — the comment is built in\n", len(delegates))
	fmt.Println("  their image, so its payload (including the leartech-report half) is unverified here:")
	for _, d := range delegates {
		fmt.Printf("       %s  (%s)\n", d.File, d.Who)
	}
	fmt.Println("       Removing an entry here does not fix a payload; it moves the finding back")
	fmt.Println("       into the silent category.")
}

func boolToCodeOK(ok bool) int {
	if ok {
		return 0
	}
	return 1
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
