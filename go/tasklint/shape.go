package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// stepKeys is every field a Tekton Step may carry. Anything else in a step is a typo or a
// spliced edit, and Tekton rejects the whole task at run time — which no YAML parse catches,
// because `resources:resources:` is a perfectly valid key with a perfectly valid value.
var stepKeys = keySet(
	"name", "image", "script", "args", "command", "env", "envFrom", "workingDir",
	"resources", "securityContext", "volumeMounts", "onError", "timeout", "stdoutConfig",
	"stderrConfig", "scriptMode", "results", "params", "workspaces",
)

// taskSpecKeys is every field a TaskSpec may carry, and pipelineSpecKeys the same for a
// PipelineSpec. Only the top level is checked; deeper structures are Tekton's business.
var taskSpecKeys = keySet(
	"params", "workspaces", "results", "steps", "volumes", "stepTemplate", "sidecars",
	"resources", "description", "displayName", "metadata", "timeout", "serviceAccountName",
)

var taskKeys = keySet("spec", "apiVersion", "kind", "metadata", "StepTemplate", "type", "description")

func keySet(kk ...string) map[string]bool {
	m := make(map[string]bool, len(kk))
	for _, k := range kk {
		m[k] = true
	}
	return m
}

// shapeFinding is one defect: where, and what.
type shapeFinding struct{ where, message string }

// checkShape walks one parsed task document and returns its findings.
//
// Two rules, both about text a splice can corrupt and a YAML parser will happily accept:
//
//	X1  a mapping key may not contain ":" — `resources:resources:` parses as the key
//	    "resources:resources" with an empty value, and Tekton then rejects the task.
//	X2  a step may only carry Tekton step fields.
func checkShape(path string, doc *yaml.Node) []shapeFinding {
	var out []shapeFinding
	var walk func(n *yaml.Node, where string)
	walk = func(n *yaml.Node, where string) {
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, c := range n.Content {
				walk(c, where)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if strings.Contains(k.Value, ":") {
					out = append(out, shapeFinding{
						where: where,
						message: fmt.Sprintf("X1 line %d: key %q contains ':' — a spliced edit, and "+
							"Tekton rejects the task for it", k.Line, k.Value),
					})
				}
				child := where
				if k.Value == "steps" && v.Kind == yaml.SequenceNode {
					for _, s := range v.Content {
						child = fmt.Sprintf("%s line %d", path, s.Line)
						checkStepKeys(path, s, &out)
					}
				}
				walk(v, child)
			}
		}
	}
	walk(doc, fmt.Sprintf("%s line %d", path, doc.Line))
	return out
}

// checkStepKeys applies X2 to one step mapping.
func checkStepKeys(path string, step *yaml.Node, out *[]shapeFinding) {
	if step.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(step.Content); i += 2 {
		k := step.Content[i]
		if strings.Contains(k.Value, ":") {
			continue // X1 already reported it
		}
		if !stepKeys[k.Value] {
			*out = append(*out, shapeFinding{
				where: fmt.Sprintf("%s line %d", path, k.Line),
				message: fmt.Sprintf("X2 step field %q is not a Tekton step field (known: %s)",
					k.Value, strings.Join(sorted(stepKeys), ", ")),
			})
		}
	}
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runShape reports every task file whose parsed shape Tekton would reject.
func runShape(root string) int {
	fmt.Println("=== every task file has the shape Tekton accepts, not merely valid YAML ===")
	// filepath.Glob has no **, so walk: a new nesting level is then covered by default.
	var files []string
	werr := filepath.Walk(filepath.Join(root, "tasks"), func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
			files = append(files, p)
		}
		return nil
	})
	if werr != nil {
		note("could not walk tasks/: %v", werr)
		return 1
	}
	sort.Strings(files)

	fail := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			note("could not read %s: %v", f, err)
			fail++
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			continue // the syntax check owns unparseable files
		}
		rel, _ := filepath.Rel(root, f)
		for _, finding := range checkShape(rel, &doc) {
			fmt.Printf("  %s\n    %s\n", finding.where, finding.message)
			fail++
		}
	}
	if fail == 0 {
		fmt.Printf("  ok   %d task file(s): no spliced keys, no unknown step fields\n", len(files))
	}
	return boolToCode(fail == 0)
}
