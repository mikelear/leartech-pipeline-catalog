// triggersync checks a CONSUMER repo's Lighthouse triggers against this catalog.
//
// The status context a PR sees ("audit", "test", "end2end") is declared in the
// consumer's .lighthouse/jenkins-x/triggers.yaml, and the comment that explains a
// red check is posted by the CATALOG task that source points at. Nothing checks
// that the two agree, and they cannot both be read from one repo:
//
//	consumer: context "audit" -> source audit.yaml -> uses: tasks/dotnet-audit
//	catalog:  tasks/dotnet-audit/pullrequest.yaml -> posts nothing at all
//
// So `audit` can go red on a PR and the PR says only "the following test failed",
// which is the console-api#31 shape generalised. This tool walks that chain end to
// end and reports where it breaks — run in the consumer (it has the triggers file)
// with a catalog checkout (it has the tasks).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type presubmit struct {
	Name    string `yaml:"name"`
	Context string `yaml:"context"`
	Source  string `yaml:"source"`
}

type triggerConfig struct {
	Spec struct {
		Presubmits []presubmit `yaml:"presubmits"`
	} `yaml:"spec"`
}

// uses is the catalog reference a source file carries. The @ref is allowed to be
// anything: pinning it is a separate decision, and a stale pin is a different
// finding from a missing task.
var uses = regexp.MustCompile(`uses:(?:mikelear/)?leartech-pipeline-catalog/(tasks/[^\s@]+)@(\S+)`)

// checkFlag is how a task names the check its comment speaks for.
var checkFlag = regexp.MustCompile(`(?:^|\s)-check\s+"?([A-Za-z0-9._-]+)`)

type Finding struct {
	Where  string
	Detail string
}

func (f Finding) String() string { return f.Where + ": " + f.Detail }

// delegates are tasks whose posting happens INSIDE their image, so no grep of the
// task can see it. Named with the file that does it, because "we could not tell"
// is only honest if it says what it could not tell and why.
var delegates = map[string]string{
	"tasks/security-scan/pullrequest.yaml":         "/app/static-scan.sh in security-tools",
	"tasks/security-scan/image-scan.yaml":          "/app/image-scan.sh in security-tools",
	"tasks/ai-review/pullrequest.yaml":             "the ai-review worker image",
	"tasks/ai-review/feedback.yaml":                "the ai-review worker image",
	"tasks/security-scan/dynamic/pullrequest.yaml": "the dynamic-scan image",
}

// taskChecks reads the -check values a catalog task posts under. Empty means the
// task posts nothing, which is a finding in itself: its red status carries no
// explanation on the PR.
func taskChecks(path string) ([]string, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- paths come from the consumer's own triggers
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range checkFlag.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out, nil
}

func run(repoDir, catalogDir string) (findings, notes []Finding, err error) {

	triggersPath := filepath.Join(repoDir, ".lighthouse", "jenkins-x", "triggers.yaml")
	b, err := os.ReadFile(triggersPath) // #nosec G304
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", triggersPath, err)
	}
	var tc triggerConfig
	if err := yaml.Unmarshal(b, &tc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", triggersPath, err)
	}
	if len(tc.Spec.Presubmits) == 0 {
		return nil, nil, fmt.Errorf("%s declares no presubmits. A repo with nothing to run "+
			"does not need this check; a file that failed to parse looks identical", triggersPath)
	}

	contexts := map[string]string{}
	for _, p := range tc.Spec.Presubmits {
		where := "presubmit " + p.Name
		if p.Context == "" {
			findings = append(findings, Finding{where, "has no context: Lighthouse would " +
				"report a status no one can name or look up"})
			continue
		}
		if prev, dup := contexts[p.Context]; dup {
			findings = append(findings, Finding{where, fmt.Sprintf(
				"reuses context %q, already used by %q — two checks share one status, so "+
					"whichever finishes last decides the colour", p.Context, prev)})
		}
		contexts[p.Context] = p.Name

		if p.Source == "" {
			findings = append(findings, Finding{where, "has no source"})
			continue
		}
		srcPath := filepath.Join(repoDir, ".lighthouse", "jenkins-x", p.Source)
		src, err := os.ReadFile(srcPath) // #nosec G304
		if err != nil {
			findings = append(findings, Finding{where, fmt.Sprintf(
				"source %q does not exist. Lighthouse fails the check with no job, and the "+
					"PR shows a status that never ran", p.Source)})
			continue
		}

		refs := uses.FindAllStringSubmatch(string(src), -1)
		if len(refs) == 0 {
			findings = append(findings, Finding{where, fmt.Sprintf(
				"source %q uses no leartech-pipeline-catalog task, so whatever it runs is "+
					"outside the catalog's checks (including this one)", p.Source)})
			continue
		}

		var checks []string
		var taskPath string
		for _, r := range refs {
			taskPath = r[1]
			tp := filepath.Join(catalogDir, r[1])
			c, err := taskChecks(tp)
			if err != nil {
				findings = append(findings, Finding{where, fmt.Sprintf(
					"source %q points at %s@%s, which this catalog does not have. Renamed, "+
						"moved, or the ref is from another branch", p.Source, r[1], r[2])})
				continue
			}
			checks = append(checks, c...)
		}

		// The finding that matters: a check that can go red and say nothing.
		if len(checks) == 0 {
			if who, ok := delegates[taskPath]; ok {
				notes = append(notes, Finding{where, fmt.Sprintf(
					"context %q is served by %s, which posts from inside %s — this tool "+
						"cannot see that, so the comment is unverified rather than absent",
					p.Context, taskPath, who)})
				continue
			}
			findings = append(findings, Finding{where, fmt.Sprintf(
				"context %q is served by %s, which posts no comment: a red %q shows only "+
					"Lighthouse's generic failure table, with the reason in a log nobody was "+
					"told to open", p.Context, taskPath, p.Context)})
			continue
		}

		matched := false
		for _, c := range checks {
			if c == p.Context {
				matched = true
				break
			}
		}
		if !matched {
			sort.Strings(checks)
			findings = append(findings, Finding{where, fmt.Sprintf(
				"context %q is served by %s, which files its comment under -check %s — no "+
					"comment carries the marker for %q, so a reader looking for that check's "+
					"explanation finds none", p.Context, taskPath, strings.Join(checks, "/"), p.Context)})
		}
	}
	return findings, notes, nil
}

func main() {
	repo := flag.String("repo", ".", "consumer repo directory (has .lighthouse/jenkins-x/triggers.yaml)")
	catalog := flag.String("catalog", ".", "leartech-pipeline-catalog checkout (has tasks/)")
	flag.Parse()

	findings, notes, err := run(*repo, *catalog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "triggersync:", err)
		os.Exit(2)
	}
	for _, n := range notes {
		fmt.Println("NOTE", n)
	}
	if len(findings) == 0 {
		fmt.Printf("==> triggersync: every context in %s resolves to a catalog task that "+
			"posts under that name\n", *repo)
		return
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	if len(findings) == 0 {
		fmt.Printf("==> triggersync: every context resolves to a task that posts under "+
			"that name (%d note(s) this tool cannot settle)\n", len(notes))
		return
	}
	fmt.Fprintf(os.Stderr, "\nFAIL: %d finding(s). Each one is a check that can be red "+
		"with no explanation on the PR.\n", len(findings))
	os.Exit(1)
}
