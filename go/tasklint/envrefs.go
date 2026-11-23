package main

import (
	"fmt"
	"regexp"
	"sort"
)

// envrefs: a step that writes ${VAR:?...} must DECLARE VAR.
//
// WHY. `${VAR:?}` is fatal: if VAR is unset or empty the shell exits on the spot, there is no
// fallback. That is a good property — a step configured wrong should stop rather than run with a
// blank — except when the step never had VAR to begin with, because then it dies at the very
// moment it was supposed to do its job.
//
// MEASURED, and it is exactly the defect class this catalog exists to remove:
//
//	leartech-console-api#37   `make lint` failed at comment-gate (2 prose lines, no test). The
//	                          report step that exists to say so died at
//	                            /tekton/scripts/script-4-sxk9x: line 10: CLUSTER_ID: parameter null or not set
//	                          and posted NOTHING. The PR showed only Lighthouse's generic "the
//	                          following test failed" — console-api#31, in the step whose whole
//	                          purpose is to prevent it.
//
// Three tasks shipped this: `go-lint`, `chart-standard` and `values-standard`. Their sibling
// tasks (`govulncheck`, `go-test`, `end2end`, …) declare CLUSTER_ID from `jx-cluster-config`, so
// the pattern was right everywhere it was copied from — and the three that forgot it were
// indistinguishable on a passing PR, because a report step only runs on failure.
//
// THE RULE: every `${VAR:?...}` in a step script must be declared in that step's `env:` or in the
// taskSpec's `stepTemplate.env:`. `envFrom` does not count: `jx-boot-job-env-vars` is `optional:
// true` and carries only DEV_/JX_REPOSITORY_ credentials, so an envFrom reference is a hope, not
// a declaration — and this check is about what is knowable from the file.
//
// proven-by: TestEnvRefsFindsAFatalVarThatIsNeverDeclared
func runEnvRefs(steps []step, loadErrs []error) int {
	fmt.Println("=== a step's fatal ${VAR:?} must be declared in its env ===")
	reportLoadErrors(loadErrs)

	fatal := regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*):\?`)

	fail, checked := len(loadErrs), 0
	var findings []string
	for _, s := range steps {
		body := stripComments(s.Script)
		names := make([]string, 0)
		for _, m := range fatal.FindAllStringSubmatch(body, -1) {
			names = append(names, m[1])
		}
		sort.Strings(names)
		for _, name := range uniqueSorted(names) {
			checked++
			if containsString(s.Env, name) {
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"%s :: step %s uses ${%s:?} and does not declare %s. The shell exits there with "+
					"'%s: parameter null or not set', so this step does its job only when the "+
					"variable happens to be set — and never when its own check fails. Declare it "+
					"(env: %s) or drop the :? and give it a default.",
				s.File, stepName(s), name, name, name, name))
			fail++
		}
	}

	fmt.Printf("  %d fatal ${VAR:?} reference(s) across %d step(s)\n", checked, len(steps))
	if len(findings) > 0 {
		fmt.Println()
		fmt.Println("FAIL these steps would die at their own guard:")
		for _, f := range findings {
			fmt.Printf("       %s\n", f)
		}
		fmt.Println()
		fmt.Println("A report step that dies is worse than no report step: the check is red, the")
		fmt.Println("PR says only 'the following test failed', and the reason is in a log nobody")
		fmt.Println("was told to open. Measured on leartech-console-api#37.")
	}
	if fail == 0 {
		fmt.Println("  ok   every fatal reference has a declaration behind it")
	}
	return boolToCode(fail == 0)
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
