package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// go test ./go/prpost -run TestGolden -update
//
// rewrites every want.md from the renderer. The drift failure tells people to regenerate
// rather than hand-edit the expectation, so regenerating has to be one command.
var update = flag.Bool("update", false, "rewrite every want.md from the renderer")

// The scenario suite.
//
// Each directory under testdata/render holds in.json (the typed input a step would supply)
// and want.md (the exact comment the forge receives). The test renders through the SAME
// function the CLI uses — renderFromInput — so a golden file cannot pass while the real post
// drifts.
//
// The scenarios are not invented. Every one of them is a message this estate has actually
// sent, or a failure it has actually had, and the name says which:
//
//	coverage-scope-matched-nothing   "0.0% (threshold 60%)" for a scope that measured nothing
//	coverage-baseline-unavailable   "Delta vs main: unavailable" permanently, for a whole forge
//	end2end-undeclared              a repo with neither a pack nor end2end/NONE
//	end2end-preview-not-ready       the timeout that read as a broken preview
//	end2end-tool-missing            "auth-refusals FAIL" for a python3 that was not installed
//	end2end-checks-skipped          "2/5 checks passed, 16 skipped" when the runner exported nothing
//
// A new renderer, or a new tool, is added by adding a directory. The test refuses to run on an
// empty set, because a golden suite that finds no scenarios passes over nothing at all.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob("testdata/render/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no scenarios under testdata/render — a golden suite over nothing always passes")
	}
	for _, dir := range dirs {
		dir := dir
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, "in.json"))
			if err != nil {
				t.Fatalf("reading in.json: %v", err)
			}
			var in input
			if err := json.Unmarshal(raw, &in); err != nil {
				t.Fatalf("in.json is not valid input: %v", err)
			}

			got := renderFromInput(in)
			wantPath := filepath.Join(dir, "want.md")

			if *update {
				if err := os.WriteFile(wantPath, []byte(got), 0o644); err != nil {
					t.Fatalf("writing want.md: %v", err)
				}
				return
			}

			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("reading want.md (run with -update to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("the comment changed for scenario %q.\n\n--- want ---\n%s\n--- got ---\n%s\n\n"+
					"If the new wording is right, run:\n  go test ./go/prpost -run TestGolden -update\n"+
					"and read the want.md diff — a sentence this estate sends to every PR should be\n"+
					"reviewed, not regenerated in a batch.",
					name, string(want), got)
			}
		})
	}
}
