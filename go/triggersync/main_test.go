package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four ways this chain breaks, each a shape that has shipped. The fixture is
// a consumer whose contexts are served by: a task that posts under the right name
// (clean), a task that posts under another name, a task that posts nothing, a
// duplicate context, and a source that does not exist.
func TestEveryChainBreakIsReported(t *testing.T) {
	findings, _, err := run(
		filepath.Join("testdata", "consumer"),
		filepath.Join("testdata", "minicatalog"),
	)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"presubmit test":    "", // clean: must produce nothing
		"presubmit audit":   "files its comment under -check mutants",
		"presubmit end2end": "posts no comment",
		"presubmit dup-a":   "reuses context",
		"presubmit missing": "does not exist",
	}
	got := map[string]string{}
	for _, f := range findings {
		got[f.Where] = f.Detail
	}
	for where, fragment := range want {
		detail, ok := got[where]
		switch {
		case fragment == "" && ok:
			t.Errorf("%s must be clean, got %q", where, detail)
		case fragment != "" && !ok:
			t.Errorf("%s produced no finding; wanted one about %q. All findings: %v",
				where, fragment, findings)
		case fragment != "" && !strings.Contains(detail, fragment):
			t.Errorf("%s: finding %q does not mention %q", where, detail, fragment)
		}
	}
	if len(findings) != 4 {
		t.Errorf("want 4 findings (the clean presubmit excluded), got %d: %v",
			len(findings), findings)
	}
}

// A triggers file with no presubmits must be an ERROR, not a clean pass: a file
// that failed to parse and a repo that genuinely runs nothing look identical, and
// passing on the second is how a check reports success over nothing.
//
// proven-by: TestNoPresubmitsIsAnError
func TestNoPresubmitsIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".lighthouse", "jenkins-x"), 0o750); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: config.lighthouse.jenkins-x.io/v1alpha1\nkind: TriggerConfig\nspec: {}\n"
	path := filepath.Join(dir, ".lighthouse", "jenkins-x", "triggers.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(dir, "testdata/minicatalog"); err == nil {
		t.Fatal("an empty trigger config was accepted; a check over nothing must not pass")
	}
}
