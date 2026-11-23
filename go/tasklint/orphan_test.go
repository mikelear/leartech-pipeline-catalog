package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeToolTree lays out the smallest catalog this check needs: a Dockerfile whose build list
// names the tools, plus whichever of tasks/, go/leartech-go.mk, .lighthouse/ and go tests a
// case wants.
func writeToolTree(t *testing.T, dockerfile string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	all := map[string]string{"checkers/Dockerfile": dockerfile}
	for k, v := range files {
		all[k] = v
	}
	for rel, body := range all {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return root
}

// baselineFor writes a baseline file and returns its path.
func baselineFor(t *testing.T, lines string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "orphan.txt")
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// dockerfileFor builds the image's build list. The loop sits on its OWN line, as it does in
// the real Dockerfile — a fixture with it mid-line would test a shape nothing parses.
func dockerfileFor(tools ...string) string {
	body := "FROM alpine\nRUN set -eu; \\\n    for m in"
	for _, t := range tools {
		body += " " + t
	}
	return body + "; do echo \"$m\"; done\n"
}

// THE FINDING: a tool with neither a caller nor a reason on file. This is the shape that cost
// four separate investigations — `argohooks`, `identitystd`, `scopedrift`, `authcoverage` — and
// every one of them passed its own tests the whole time.
//
// proven-by: TestOrphanFindsAToolWithNoCaller
func TestOrphanFindsAToolWithNoCaller(t *testing.T) {
	root := writeToolTree(t, dockerfileFor("calledtool", "testedtool", "alonetool"), map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("lint", "#!/bin/sh\ncalledtool -check\n"),
		"go/testedtool/main_test.go":  "package main\n// testedtool is exercised here, and called nowhere\n",
	})
	if code := runOrphan(root, baselineFor(t, "")); code == 0 {
		t.Fatal("a tool with no caller and no reason on file passed")
	}
	// A tool that is TESTED but not called is a different finding from one nobody has
	// thought about: the reason on file is what turns an accident into a decision.
	if code := runOrphan(root, baselineFor(t, "alonetool|no caller by decision\n")); code != 0 {
		t.Fatal("a recorded reason was rejected")
	}
}

// A mention is not a call. These are the near-misses the neighbouring guards each paid for:
// a comment, a URL path, and a word inside another word.
//
// proven-by: TestOrphanDoesNotCountMentions
func TestOrphanDoesNotCountMentions(t *testing.T) {
	root := writeToolTree(t, dockerfileFor("mentionedtool"), map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("lint", `#!/bin/sh
# mentionedtool is not run here
echo "see raw.githubusercontent.com/x/go/mentionedtool/main.go" >&2
echo prementionedtool
`),
	})
	if code := runOrphan(root, baselineFor(t, "")); code == 0 {
		t.Fatal("a comment, a URL path or a substring counted as a caller")
	}
}

// ...and each of the four real kinds of caller counts on its own.
func TestOrphanAcceptsEveryKindOfCaller(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"a task step": {"tasks/demo/pullrequest.yaml": taskYAML("lint", "#!/bin/sh\na -x\n")},
		"the mk":      {"go/leartech-go.mk": "lint:\n\tb -root .\n"},
		"our own CI":  {".lighthouse/jenkins-x/lint.yaml": "script: |\n  c -check\n"},
		"a Go test":   {"go/c/main_test.go": "package main\n// c is exercised here\n"},
	} {
		t.Run(name, func(t *testing.T) {
			root := writeToolTree(t, dockerfileFor("a", "b", "c"), files)
			// Waive the two this case does not exercise; a stale line for the one it DOES
			// is the other test's business.
			waive := ""
			for _, tool := range []string{"a", "b", "c"} {
				if _, ok := files["go/c/main_test.go"]; ok && tool == "c" {
					continue
				}
				if _, ok := files["tasks/demo/pullrequest.yaml"]; ok && tool == "a" {
					continue
				}
				if _, ok := files["go/leartech-go.mk"]; ok && tool == "b" {
					continue
				}
				if _, ok := files[".lighthouse/jenkins-x/lint.yaml"]; ok && tool == "c" {
					continue
				}
				waive += tool + "|waived for this case\n"
			}
			if code := runOrphan(root, baselineFor(t, waive)); code != 0 {
				t.Fatalf("a tool called by %s was reported orphaned (exit %d)", name, code)
			}
		})
	}
}

// A baseline line whose tool gained a caller is stale, and staleness fails: leaving it
// implies the decision still stands, and the next reader believes it does.
//
// proven-by: TestOrphanFailsOnAStaleBaselineLine
func TestOrphanFailsOnAStaleBaselineLine(t *testing.T) {
	root := writeToolTree(t, dockerfileFor("calledtool"), map[string]string{
		"tasks/demo/pullrequest.yaml": taskYAML("lint", "#!/bin/sh\ncalledtool -check\n"),
	})
	if code := runOrphan(root, baselineFor(t, "calledtool|no longer true\n")); code == 0 {
		t.Fatal("a stale baseline line passed")
	}
}

// A Dockerfile with no build list is a failure, not a clean tree: zero tools checked must
// never read as zero orphans. That is the "0 checked, ok" defect the other guards each
// needed an anchor for.
//
// proven-by: TestOrphanRefusesAnUnreadableToolList
func TestOrphanRefusesAnUnreadableToolList(t *testing.T) {
	root := writeToolTree(t, "FROM alpine\nRUN echo no build loop here\n", nil)
	if code := runOrphan(root, baselineFor(t, "")); code == 0 {
		t.Fatal("no build list read as no orphans")
	}
}
