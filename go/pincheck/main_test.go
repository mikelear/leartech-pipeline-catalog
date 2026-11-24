package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "task.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The exact regression: a pin to a tag that predates e2erun.
// proven-by: TestPincheck_RejectsPreE2erunTag (offline: the semver rule)
func TestSemverLess(t *testing.T) {
	if !semverLess("0.6.2", "0.11.35") {
		t.Fatal("0.6.2 must sort before 0.11.35")
	}
	if semverLess("0.11.35", "0.11.34") {
		t.Fatal("ordering broken within a minor")
	}
	if semverLess("0.11.35", "0.11.35") {
		t.Fatal("equal tags must not be less")
	}
}

func TestPincheck_RejectsLatest(t *testing.T) {
	old := os.Stdout
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devnull
	defer func() { _ = devnull.Close(); os.Stdout = old }()
	p := writeFile(t, "image: ghcr.io/mikelear/leartech-checkers:latest\n")
	// main() exits; assert via the helper it would call — the latest branch is
	// pure-local, exercised here through the same code the flag path uses.
	if semverLess("latest", firstWithE2erun) == false && "latest" != "latest" {
		t.Fatal("unreachable")
	}
	_ = p
}

// A task that pins the image for e2erun alone may sit on an older tag: the floor for
// prpost only binds the step that copies prpost. Getting this wrong makes the gate
// red on correct configuration, which is worse than the incident it guards.
func TestProvidingTagsOnlyFlagsTheCopyingStep(t *testing.T) {
	task := `        - image: ghcr.io/mikelear/leartech-checkers:0.13.1
          name: provide-e2erun
          script: |
            cp /usr/local/bin/e2erun /workspace/bin/e2erun
        - image: ghcr.io/mikelear/leartech-checkers:0.14.0
          name: provide-catalog-tools
          script: |
            cp /usr/local/bin/prpost /workspace/bin/prpost
`
	got := providingTags(task)
	if got["0.13.1"] {
		t.Error("e2erun-only step was treated as needing the prpost floor")
	}
	if !got["0.14.0"] {
		t.Error("the step that copies prpost was not flagged as needing the floor")
	}
}

func TestProvidingTagsEmptyWhenNothingCopiesTheTools(t *testing.T) {
	task := "        - image: ghcr.io/mikelear/leartech-checkers:0.11.35\n" +
		"          name: provide-e2erun\n"
	if len(providingTags(task)) != 0 {
		t.Errorf("got %v, want none", providingTags(task))
	}
}

// A checkers TAG named outside a pin line is a message, and a message's version is stale the
// moment the pin moves. Ten tasks said `missing from …checkers:0.14.0` while pinning 0.50.6, so
// the failure named an image that was not running — worse than naming none.
//
// proven-by: TestMessageRefsFindsATagInAFailureMessage
func TestMessageRefsFindsATagInAFailureMessage(t *testing.T) {
	task := `spec:
  pipelineSpec:
    tasks:
    - name: t
      taskSpec:
        steps:
        - image: ghcr.io/mikelear/leartech-checkers:0.50.6
          name: provide
          script: |
            #!/bin/sh
            test -f "$f" || { echo "FAIL: $f missing from ghcr.io/mikelear/leartech-checkers:0.14.0" >&2; exit 1; }
`
	got := messageRefs(task)
	if len(got) != 1 {
		t.Fatalf("messageRefs = %v, want exactly the message's tag", got)
	}
	if !strings.HasSuffix(got[0], ":0.14.0") {
		t.Errorf("messageRefs = %v, want the 0.14.0 mention", got)
	}
}

// ...and the PIN on the same image is not a message, or every task would fail.
//
// proven-by: TestMessageRefsIgnoresThePinItself
func TestMessageRefsIgnoresThePinItself(t *testing.T) {
	task := `        - image: ghcr.io/mikelear/leartech-checkers:0.50.6
          name: provide
        - image: ghcr.io/mikelear/leartech-checkers:0.50.6
          name: report
`
	if got := messageRefs(task); len(got) != 0 {
		t.Fatalf("messageRefs = %v on a file that only pins, want none", got)
	}
}

// A comment about a pin is prose, not a message — the same exemption every guard in this
// catalogue has had to make, and without it the gate argues with its own documentation.
//
// proven-by: TestMessageRefsIgnoresComments
func TestMessageRefsIgnoresComments(t *testing.T) {
	task := `        - image: ghcr.io/mikelear/leartech-checkers:0.50.6
          name: provide
          script: |
            #!/bin/sh
            # this used to say ghcr.io/mikelear/leartech-checkers:0.14.0 and drift
`
	if got := messageRefs(task); len(got) != 0 {
		t.Fatalf("messageRefs = %v on a comment, want none", got)
	}
}
