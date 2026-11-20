package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Two more single-question checks, both of which used to be python heredocs that skipped
// in the golang `test` image and so never ran where the pipeline runs.

// ── phases: an end2end phase runs where its tools are guaranteed ─────────────────────
//
// A Tekton task's steps are separate containers that share only /workspace, so a tool
// installed in one step is not in the next. The end2end task used to apt-get curl and
// download kubectl in its FIRST step and then run four more in plain debian:12-slim —
// where neither exists. The wait phase therefore reported "kubectl or PULL_PULL_SHA
// unavailable" and probed a Service URL it had never resolved, and the failure read as a
// broken preview rather than a container with no curl.
var (
	phaseRuntimeOK = []string{"leartech-checkers", "playwright-runner"}
	installsAtRun  = []string{"apt-get install", "apk add", "dl.k8s.io"}
	// Phases that wait on a preview, and the tool each one must at least mention.
	mustMention = map[string]string{
		"end2end-await": "kubectl", // resolve the Service, confirm the commit
	}
	mustMentionAny = map[string]string{
		"end2end-await": "curl",
		"end2end-env":   "curl",
		"end2end-ui":    "curl",
	}
	// The env phase must ASSERT its tools: a missing one otherwise shows up three steps
	// later as a broken preview.
	mustAssertTools = "end2end-env"
)

// proven-by: TestPhasesRejectsAPhaseThatInstallsAtRunTime
func runPhases(dir string) int {
	fmt.Println("=== end2end phases run somewhere with their tools ===")
	fail := 0
	for _, f := range []string{"tasks/end2end/pullrequest.yaml", "tasks/end2end-ui/pullrequest.yaml"} {
		// An ABSENT file is not a finding: a fixture for one lane should not have to
		// invent an empty file for the other. A file that exists and does not parse IS.
		if _, statErr := os.Stat(filepath.Join(dir, f)); statErr != nil {
			continue
		}
		steps, err := stepsOfFile(filepath.Join(dir, f))
		if err != nil {
			note("%s: %v", f, err)
			fail++
			continue
		}
		var phases []step
		for _, s := range steps {
			if strings.HasPrefix(s.Name, "end2end") {
				phases = append(phases, s)
			}
		}
		if len(phases) == 0 {
			continue
		}
		images := map[string]bool{}
		for _, s := range phases {
			img := s.Image
			images[img] = true

			okRuntime := false
			for _, want := range phaseRuntimeOK {
				if strings.Contains(img, want) {
					okRuntime = true
				}
			}
			if !okRuntime {
				note("%s :: %s runs in %q, which carries no guarantee of curl or kubectl", f, s.Name, img)
				fail++
			}
			for _, bad := range installsAtRun {
				if strings.Contains(s.Script, bad) {
					note("%s :: %s does %q at run time; the image should carry the tool so the next phase has it too",
						f, s.Name, bad)
					fail++
				}
			}
			if tool, ok := mustMention[s.Name]; ok && !strings.Contains(s.Script, tool) {
				note("%s :: %s never mentions %s, so it cannot resolve the preview's Service or confirm the commit",
					f, s.Name, tool)
				fail++
			}
			if tool, ok := mustMentionAny[s.Name]; ok && !strings.Contains(s.Script, tool) {
				note("%s :: %s never mentions %s, but it waits on a preview", f, s.Name, tool)
				fail++
			}
			if s.Name == mustAssertTools && !strings.Contains(s.Script, "command -v") {
				note("%s :: %s does not ASSERT its tools; a missing one then shows up three steps later as a broken preview",
					f, s.Name)
				fail++
			}
		}
		if len(images) > 1 && !anyContains(images, "playwright-runner") {
			note("%s: the phases run in %d different images (%s); one runtime means one set of tools",
				f, len(images), strings.Join(sortedKeys(images), ", "))
			fail++
		} else {
			fmt.Printf("  ok   %s: %d phase(s), all in %s\n", f, len(phases), strings.Join(sortedKeys(images), ", "))
		}
	}
	if fail > 0 {
		fmt.Println()
		fmt.Println("A phase whose container lacks its tools fails plausibly: it reports the")
		fmt.Println("preview as broken. Assert the tool, and run where it is guaranteed.")
		return 1
	}
	fmt.Println("  ok   every phase has its tools")
	return 0
}

// ── renders: every task that renders a chart also judges it ──────────────────────────
//
// `helm template` proves a chart PARSES. chartstd proves it says what the standard
// requires. A task that renders and does not judge is a green check over half the
// question, which is why the umbrellas passed for weeks while carrying tenant identity.
// proven-by: TestRendersFailsOnATaskThatRendersAndJudgesNothing
func runRenders(dir, baselinePath string) int {
	fmt.Println("=== every task that renders a chart also judges it ===")
	baseline, err := readSet(baselinePath)
	if err != nil {
		note("%v", err)
		return 1
	}
	files, err := taskFiles(dir)
	if err != nil {
		note("%v", err)
		return 1
	}
	var rendersWithoutJudging []string
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f)) // #nosec G304 -- walked from dir
		if err != nil {
			note("%s: %v", f, err)
			continue
		}
		src := string(b)
		if !strings.Contains(src, "helm template") || strings.Contains(src, "chartstd") {
			continue
		}
		rendersWithoutJudging = append(rendersWithoutJudging, f)
	}
	fmt.Printf("  %d task(s) run `helm template` and never call chartstd\n\n", len(rendersWithoutJudging))

	bad := 0
	var fixed []string
	for _, f := range sortedKeys(baseline) {
		if !containsString(rendersWithoutJudging, f) {
			fixed = append(fixed, f)
		}
	}
	if len(fixed) > 0 {
		fmt.Println("  NOTE these now judge what they render — delete their line from the baseline:")
		for _, f := range fixed {
			fmt.Printf("       %s\n", f)
		}
	}
	var unseen []string
	for _, f := range rendersWithoutJudging {
		if !baseline[f] {
			unseen = append(unseen, f)
		}
	}
	if len(unseen) > 0 {
		fmt.Println("FAIL these tasks render a chart and judge nothing:")
		for _, f := range unseen {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
		fmt.Println("Add a chartstd step (see tasks/helm/pullrequest.yaml), or record why the task")
		fmt.Println("renders without judging in")
		fmt.Printf("  %s\n", baselinePath)
		bad++
	}
	if len(fixed) > 0 {
		bad++
	}
	if bad == 0 {
		fmt.Println("  ok   every rendering task judges what it renders")
	}
	return boolToCode(bad == 0)
}

// stepsOfFile reads one task file's steps with the image, which stepsOf in failures.go
// does not carry. Kept here because only these checks need the image.
func stepsOfFile(path string) ([]step, error) {
	steps, errs := loadStepsFromFiles([]string{path})
	if len(errs) > 0 {
		return nil, errs[0]
	}
	return steps, nil
}

func anyContains(set map[string]bool, needle string) bool {
	for k := range set {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}
