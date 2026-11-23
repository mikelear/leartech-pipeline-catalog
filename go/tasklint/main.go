// tasklint — the checks over this catalog's own task YAML, as one program.
//
// WHY THIS REPLACES FIVE PYTHON HEREDOCS. Each of those checks walked tasks/**/*.yaml for
// a different fact, and each needed PyYAML. The `test` presubmit runs in golang:1.27.4,
// which has neither python3 nor pyyaml, so every one of them printed
// "SKIP: no pyyaml in this image" and exited 0 (measured on catalog#359's own run: four
// SKIPs). A guard that cannot run where the pipeline runs is not a guard.
//
// One program, one `go test`, and the same binary can ship in the checkers image for a
// task step. The subcommands are named after the question each answers:
//
//	syntax    does every step script parse?            (task_syntax_test.sh)
//	message   does a task compose prose in shell?      (message_layer_test.sh)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// step is one `steps:` entry of one task, with the file and name it came from. Only the
// fields these checks read; a task YAML has far more and none of it changes the answer.
type step struct {
	File    string
	Task    string
	Name    string
	Script  string
	Image   string
	OnError string
	// Env is this step's OWN `env:` names; TemplateEnv is the taskSpec's `stepTemplate.env:`.
	// They are kept apart because the two rules that read them differ: a step may legitimately
	// override a template default, but it may not declare the same name twice ITSELF.
	Env         []string
	TemplateEnv []string
}

func (s step) where() string {
	if s.Name == "" {
		return s.File
	}
	return s.File + " :: " + s.Name
}

// loadSteps reads every tasks/**/*.yaml under dir and returns each step. A file that does
// not parse is returned as an error rather than skipped: a task whose YAML is broken is
// exactly what these checks exist to catch, and skipping it would make every check pass
// over a smaller population.
func loadSteps(dir string) ([]step, []error) {
	matches, err := filepath.Glob(filepath.Join(dir, "tasks", "*", "*.yaml"))
	if err != nil {
		return nil, []error{err}
	}
	// One level deeper covers tasks/security-scan/dynamic/pullrequest.yaml and the like.
	deeper, err := filepath.Glob(filepath.Join(dir, "tasks", "*", "*", "*.yaml"))
	if err == nil {
		matches = append(matches, deeper...)
	}
	sort.Strings(matches)

	rel := make([]string, 0, len(matches))
	for _, path := range matches {
		r, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			r = path
		}
		// Normalise to forward slashes so a baseline written on one machine matches the
		// same file on another.
		rel = append(rel, filepath.ToSlash(r))
	}
	return loadStepsFromFiles(matches, dir)
}

// loadStepsFromFiles parses an explicit list of task files. paths are the absolute paths
// read; names are what a finding prints (usually the same paths relative to the catalog
// root). Splitting it out lets a check read ONE task file without inventing a tree.
func loadStepsFromFiles(paths []string, baseDir ...string) ([]step, []error) {
	var out []step
	var errs []error
	for _, path := range paths {
		name := path
		if len(baseDir) > 0 && baseDir[0] != "" {
			if r, rerr := filepath.Rel(baseDir[0], path); rerr == nil {
				name = filepath.ToSlash(r)
			}
		}
		b, err := os.ReadFile(path) // #nosec G304 -- the operator names the tree
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		type envVar struct {
			Name string `yaml:"name"`
		}
		var doc struct {
			Spec struct {
				PipelineSpec struct {
					Tasks []struct {
						Name     string `yaml:"name"`
						TaskSpec struct {
							StepTemplate struct {
								Env []envVar `yaml:"env"`
							} `yaml:"stepTemplate"`
							Steps []struct {
								Name    string   `yaml:"name"`
								Script  string   `yaml:"script"`
								Image   string   `yaml:"image"`
								OnError string   `yaml:"onError"`
								Env     []envVar `yaml:"env"`
							} `yaml:"steps"`
						} `yaml:"taskSpec"`
					} `yaml:"tasks"`
				} `yaml:"pipelineSpec"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(b, &doc); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		for _, t := range doc.Spec.PipelineSpec.Tasks {
			for _, s := range t.TaskSpec.Steps {
				own := make([]string, 0, len(s.Env))
				for _, e := range s.Env {
					own = append(own, e.Name)
				}
				tmpl := make([]string, 0, len(t.TaskSpec.StepTemplate.Env))
				for _, e := range t.TaskSpec.StepTemplate.Env {
					tmpl = append(tmpl, e.Name)
				}
				out = append(out, step{
					File: name, Task: t.Name, Name: s.Name,
					Script: s.Script, Image: s.Image, OnError: s.OnError,
					Env: own, TemplateEnv: tmpl,
				})
			}
		}
	}
	return out, errs
}

func main() {
	dir := flag.String("dir", ".", "catalog checkout root (has tasks/)")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "tasklint: a subcommand is required (syntax, message)\n")
		os.Exit(2)
	}

	steps, loadErrs := loadSteps(*dir)
	var code int
	switch sub := flag.Arg(0); sub {
	case "syntax":
		code = runSyntax(steps, loadErrs)
	case "payload":
		fs := flag.NewFlagSet("payload", flag.ExitOnError)
		delegates := fs.String("delegates", "test/go/delegated_posters.txt",
			"posting paths this check cannot see, `<file>|<who posts>` per line")
		anchor := fs.Int("anchor", defaultPayloadAnchor,
			"the fewest prpost call sites a real catalog has; a drop means the matcher regressed")
		_ = fs.Parse(flag.Args()[1:])
		code = runPayload(steps, loadErrs, resolve(*dir, *delegates), *anchor)
	case "failures":
		fs := flag.NewFlagSet("failures", flag.ExitOnError)
		baseline := fs.String("baseline", "test/go/failure_report_baseline.txt",
			"the ratchet baseline: one task path per line")
		delegates := fs.String("delegates", "test/go/delegated_posters.txt",
			"posting paths this check cannot see, `<file>|<who posts>` per line")
		floor := fs.Int("delegate-floor", defaultDelegateAnchor,
			"the fewest delegated posters a real catalog has; the list only shrinks by a decision")
		_ = fs.Parse(flag.Args()[1:])
		code = runFailures(*dir, resolve(*dir, *baseline), resolve(*dir, *delegates), *floor)
	case "envrefs":
		code = runEnvRefs(steps, loadErrs)
	case "tools":
		fs := flag.NewFlagSet("tools", flag.ExitOnError)
		baseline := fs.String("baseline", "test/go/tool_callers.txt",
			"catalog binaries called from a step outside the checkers image, `<file>::<step>::<bin>|<why>`")
		_ = fs.Parse(flag.Args()[1:])
		code = runTools(*dir, resolve(*dir, *baseline))
	case "replay":
		fs := flag.NewFlagSet("replay", flag.ExitOnError)
		fixtures := fs.String("fixtures", "test/replay", "recorded PRs, one directory each")
		_ = fs.Parse(flag.Args()[1:])
		code = runReplay(*dir, resolve(*dir, *fixtures))
	case "orphan":
		fs := flag.NewFlagSet("orphan", flag.ExitOnError)
		baseline := fs.String("baseline", "test/go/orphan_tools.txt",
			"tools that run nowhere, `<tool>|<why>` per line")
		_ = fs.Parse(flag.Args()[1:])
		code = runOrphan(*dir, resolve(*dir, *baseline))
	case "inlined":
		code = runInlined(*dir)
	case "phases":
		code = runPhases(*dir)
	case "renders":
		fs := flag.NewFlagSet("renders", flag.ExitOnError)
		baseline := fs.String("baseline", "test/go/chartstd_runs_baseline.txt",
			"the ratchet baseline: one task path per line")
		_ = fs.Parse(flag.Args()[1:])
		code = runRenders(*dir, resolve(*dir, *baseline))
	case "message":
		fs := flag.NewFlagSet("message", flag.ExitOnError)
		baseline := fs.String("baseline", "test/go/message_layer_baseline.txt",
			"the ratchet baseline: <markdown> <echo> <path> per line")
		_ = fs.Parse(flag.Args()[1:])
		code = runMessage(steps, loadErrs, resolve(*dir, *baseline))
	default:
		fmt.Fprintf(os.Stderr, "tasklint: unknown subcommand %q\n", sub)
		code = 2
	}
	os.Exit(code)
}

// note prints one finding in the shape the shell tests print, so a caller reading CI logs
// sees the same indentation it did before the checks moved into Go.
func note(format string, args ...any) {
	fmt.Printf("  FAIL %s\n", fmt.Sprintf(format, args...))
}

func reportLoadErrors(errs []error) {
	for _, err := range errs {
		note("%v", err)
	}
}

// countSteps exists so a caller can print the population it judged. A check that reports
// "ok" over zero files reads as coverage and is not.
func countSteps(steps []step) int { return len(steps) }

// resolve makes a flag path relative to the catalog root rather than to the caller's
// working directory, so `-dir /repo -baseline test/go/x.txt` means /repo/test/go/x.txt
// wherever the command is run from.
func resolve(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
