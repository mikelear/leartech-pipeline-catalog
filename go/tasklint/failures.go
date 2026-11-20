package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// delegate is one posting path that is NOT visible in tasks/**/*.yaml: the comment is
// built inside the image the step calls. No catalog rule can read that, so the honest
// report says "unverified" rather than listing it as a silent failure — the same
// distinction triggersync draws with a NOTE.
type delegate struct {
	File string
	Who  string
}

// readDelegates parses `test/go/delegated_posters.txt`: `<file>|<who posts>` per line.
// ONE source for two checks (this file and the payload check), because two copies of the
// list is the defect both exist to catch, one level up.
func readDelegates(path string) ([]delegate, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the list
	if err != nil {
		return nil, fmt.Errorf("%s: %w (the checks that read it cannot say what they cannot see)", path, err)
	}
	var out []delegate
	for i, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		file, who, ok := strings.Cut(t, "|")
		if !ok || strings.TrimSpace(file) == "" {
			return nil, fmt.Errorf("%s:%d: want `<file>|<who posts>`, got %q", path, i+1, t)
		}
		out = append(out, delegate{File: strings.TrimSpace(file), Who: strings.TrimSpace(who)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// A task "can fail" if a step script exits non-zero. A task "reports" if it calls one of
// the posting tools, wherever in the file that call sits — the branch analysis belongs to
// the person fixing it, whose PR will show the comment or not.
//
// A task can fail two ways, and the narrow definition only saw the first:
//
//	explicit  `exit 1` — a branch the author wrote
//	implicit  `set -e` and a command that fails — how MOST tasks fail
//
// tasks/dotnet and tasks/dotnet-lint fail from their build command under `set -e`, so
// they were invisible to this guard while triggersync reported them correctly.
var (
	failsExplicitly = regexp.MustCompile(`(?m)^\s*exit\s+[1-9]`)
	setsErrExit     = regexp.MustCompile(`(?m)^\s*set\s+-[a-z]*e`)
	postsSomething  = regexp.MustCompile(`\b(prpost|post_sticky|post_report|UI_RENDER)\b`)
)

// delegateAnchor is how many posting paths must still be listed. The list can only SHRINK
// by a decision, and without an anchor deleting a line moves a finding back into the
// silent category while the count simply reads 4 instead of 5.
const defaultDelegateAnchor = 5

// proven-by: TestFailuresFlagsATaskThatExitsAndPostsNothing
func runFailures(dir, baselinePath, delegatesPath string, delegateFloor int) int {
	fmt.Println("=== a task that fails must tell the PR why (ratchet) ===")

	baseline, err := readSet(baselinePath)
	if err != nil {
		note("%v", err)
		return 1
	}
	delegates, derr := readDelegates(delegatesPath)
	if derr != nil {
		note("%v", derr)
		return 1
	}
	delegateSet := map[string]bool{}
	for _, d := range delegates {
		delegateSet[d.File] = true
	}
	if len(delegates) < delegateFloor {
		note("only %d delegated poster(s) listed, expected %d", len(delegates), delegateFloor)
		fmt.Printf("     %s\n", delegatesPath)
		fmt.Println("     A removed entry does not fix a payload; it makes the task look silent.")
		return 1
	}

	files, err := taskFiles(dir)
	if err != nil {
		note("%v", err)
		return 1
	}

	var explicitSilent, implicitSilent []string
	bad, pipelines := 0, 0
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(dir, f)) // #nosec G304 -- walked from dir
		if err != nil {
			note("%s: %v", f, err)
			bad++
			continue
		}
		src := string(body)
		if !strings.Contains(src, "kind: PipelineRun") {
			continue
		}
		// The denominator is tasks that can fail, not files that exist: a Task template
		// (kind: Task) has no pipelineSpec and no steps to report from.
		pipelines++
		if postsSomething.MatchString(src) {
			continue
		}
		switch {
		case failsExplicitly.MatchString(src):
			explicitSilent = append(explicitSilent, f)
		case setsErrExit.MatchString(src):
			if !delegateSet[f] {
				implicitSilent = append(implicitSilent, f)
			}
		}
	}

	fmt.Printf("  %d of %d task(s) exit explicitly and post nothing (the ratchet below)\n",
		len(explicitSilent), pipelines)
	fmt.Printf("  %d task(s) post from INSIDE their image, so no catalog rule can see it — "+
		"unverified, not silent:\n", len(delegates))
	for _, d := range delegates {
		fmt.Printf("       %s  (%s)\n", d.File, d.Who)
	}
	fmt.Println()

	if len(implicitSilent) > 0 {
		// An inventory, never a failure. These fail from a command under `set -e` with no
		// literal `exit 1`; failing on all of them at once would make the ratchet a cliff
		// nobody climbs.
		fmt.Printf("  %d more fail IMPLICITLY (set -e, no literal exit) and post nothing — "+
			"same symptom, not yet ratcheted:\n", len(implicitSilent))
		for _, f := range implicitSilent {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
	}

	var newSilent, fixed []string
	for _, f := range explicitSilent {
		if !baseline[f] {
			newSilent = append(newSilent, f)
		}
	}
	for _, f := range sortedKeys(baseline) {
		if !containsString(explicitSilent, f) {
			fixed = append(fixed, f)
		}
	}

	if len(fixed) > 0 {
		fmt.Println("  NOTE these now report — delete their line from the baseline:")
		for _, f := range fixed {
			fmt.Printf("       %s\n", f)
		}
	}
	if len(newSilent) > 0 {
		fmt.Println("FAIL the set of silent failures grew:")
		for _, f := range newSilent {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
		fmt.Println("A step that fails without telling the PR is the shape that made")
		fmt.Println("console-api#31 unreadable: the reason was in the log, the PR said")
		fmt.Println("\"the following test failed\". Give the task a report on its failure")
		fmt.Println("branch (tasks/go-lint/pullrequest.yaml is the worked example), or record")
		fmt.Println("why it cannot have one by adding its line to")
		fmt.Printf("  %s\n", baselinePath)
		bad++
	}
	if len(fixed) > 0 {
		bad++
	}

	// A report step is only reachable if the step it reports on does not stop the task.
	// Tekton abandons the remaining steps the moment one exits non-zero, so a task with a
	// report step and no `onError: continue` posts nothing — and reads, from the file,
	// exactly like one that works.
	var unreachable []string
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(dir, f)) // #nosec G304 -- walked from dir
		if err != nil {
			continue
		}
		if !hasUnreachableReport(body) {
			continue
		}
		unreachable = append(unreachable, f)
	}
	if len(unreachable) > 0 {
		fmt.Println("FAIL these tasks have a report step that can never run:")
		for _, f := range unreachable {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
		fmt.Println("Add `onError: continue` to the step the report follows.")
		bad++
	} else {
		fmt.Println("  ok   every report step is reachable")
	}

	if bad == 0 {
		fmt.Println("  ok   no new task fails without reporting it")
	}
	return boolToCode(bad == 0)
}

// hasUnreachableReport reports whether a task has a step literally named `report` while no
// step carries `onError: continue`.
func hasUnreachableReport(body []byte) bool {
	var doc struct {
		Spec struct {
			PipelineSpec struct {
				Tasks []struct {
					TaskSpec struct {
						Steps []struct {
							Name    string `yaml:"name"`
							OnError string `yaml:"onError"`
						} `yaml:"steps"`
					} `yaml:"taskSpec"`
				} `yaml:"tasks"`
			} `yaml:"pipelineSpec"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return false // a file that does not parse is the syntax check's finding
	}
	for _, t := range doc.Spec.PipelineSpec.Tasks {
		hasReport, hasOnError := false, false
		for _, s := range t.TaskSpec.Steps {
			if s.Name == "report" {
				hasReport = true
			}
			if s.OnError == "continue" {
				hasOnError = true
			}
		}
		if hasReport && !hasOnError {
			return true
		}
	}
	return false
}

// taskFiles lists tasks/**/*.yaml relative to dir, in sorted order.
func taskFiles(dir string) ([]string, error) {
	var out []string
	for _, pat := range []string{"tasks/*/*.yaml", "tasks/*/*/*.yaml"} {
		m, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			return nil, err
		}
		for _, p := range m {
			rel, rerr := filepath.Rel(dir, p)
			if rerr != nil {
				rel = p
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	sort.Strings(out)
	return out, nil
}

// readSet reads a baseline of one path per line, skipping blanks and comments.
func readSet(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the baseline
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out[t] = true
	}
	return out, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
