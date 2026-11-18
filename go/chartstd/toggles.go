package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// proven-by: TestToggleMustRemoveTheObject
//
// The toggle declaration: the off-switches a chart claims it has, and the object
// each one names.
//
// WHY THIS EXISTS. mikelear/leartech-console-api#32: `enabled: false` rendered the
// ExternalSecret anyway, because sprig's `default` treats false as empty. A single
// render looks identical whether or not the switch works: the only difference is
// which value the author happened to try. So the chart declares the switch, the
// task renders it off, and this file compares the two.
//
//	# charts/<chart>/chart-standard-toggles.yaml
//	toggles:
//	  - path: database.externalSecret.enabled   # what a consumer sets
//	    off: "false"                            # the value that must remove it
//	    removes: ExternalSecret/leartech-console-api-db
type toggle struct {
	Path    string `yaml:"path"`
	Off     string `yaml:"off"`
	Removes string `yaml:"removes"`
	Why     string `yaml:"why"`
}

type toggleDoc struct {
	Toggles []toggle `yaml:"toggles"`
}

func loadToggles(path string) ([]toggle, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the declaration
	if err != nil {
		return nil, err
	}
	var d toggleDoc
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i, t := range d.Toggles {
		if t.Path == "" || t.Removes == "" {
			return nil, fmt.Errorf("%s: toggles[%d] needs both `path` and `removes` "+
				"(off defaults to \"false\")", path, i)
		}
		if !strings.Contains(t.Removes, "/") {
			return nil, fmt.Errorf("%s: toggles[%d].removes must be Kind/name, got %q",
				path, i, t.Removes)
		}
	}
	return d.Toggles, nil
}

// objKey is how a toggle names one object.
func objKey(o obj) string { return o.Kind + "/" + o.Name }

func hasObject(docs []obj, key string) bool {
	for _, o := range docs {
		if objKey(o) == key {
			return true
		}
	}
	return false
}

// checkToggles applies T1 and T2. offs[i] is the render with decl[i] flipped.
func checkToggles(declPath string, decl []toggle, base []obj, offs [][]obj) []Finding {
	var out []Finding
	for i, t := range decl {
		where := fmt.Sprintf("%s: toggle %s=%s", declPath, t.Path, t.Off)

		if !hasObject(base, t.Removes) {
			out = append(out, Finding{"T1", where, fmt.Sprintf(
				"declares it removes %s, but that object is NOT in the default render. "+
					"The declaration is stale, the object was renamed, or the default "+
					"values already have the switch off — in which case nothing is proven.",
				t.Removes)})
			continue
		}
		if hasObject(offs[i], t.Removes) {
			out = append(out, Finding{"T2", where, fmt.Sprintf(
				"%s STILL renders with the switch off. The value cannot express the "+
					"off state, so a consumer setting it gets the object anyway — the "+
					"external-secret keeps a second owner and the cluster keeps a "+
					"credential nobody meant to create.", t.Removes)})
		}
	}
	return out
}

// proven-by: TestToggleMustRemoveTheObject
//
// undeclaredSwitches reports boolean `enabled` keys in a chart's values that no
// toggle declaration covers.
//
// A YAML walk, not a line scan: the first version tracked indentation and produced
// `enabled.externalSecret.enabled` for a key that is
// `database.externalSecret.enabled`, because a two-level parent stack does not
// follow a file with several branches. A wrong path in the output is worse than no
// output at all.
func undeclaredSwitches(chartDir string, decl []toggle) []Finding {
	declared := map[string]bool{}
	for _, t := range decl {
		declared[t.Path] = true
	}
	b, err := os.ReadFile(filepath.Join(chartDir, "values.yaml")) // #nosec G304
	if err != nil {
		return nil
	}
	var vals map[string]any
	if err := yaml.Unmarshal(b, &vals); err != nil {
		return []Finding{{"T3", chartDir + " values.yaml", fmt.Sprintf(
			"cannot be parsed (%v), so its off-switches cannot be enumerated", err)}}
	}

	var notes []Finding
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			switch val := v.(type) {
			case map[string]any:
				walk(path, val)
			case bool:
				if (k == "enabled" || strings.HasSuffix(k, "Enabled")) && !declared[path] {
					notes = append(notes, Finding{"T3", chartDir + " values.yaml", fmt.Sprintf(
						"%s = %t is an off-switch nobody has flipped in a test. Add it to "+
							"chart-standard-toggles.yaml with the object it must remove.",
						path, val)})
				}
			}
		}
	}
	walk("", vals)

	sort.Slice(notes, func(i, j int) bool { return notes[i].Detail < notes[j].Detail })
	return notes
}
