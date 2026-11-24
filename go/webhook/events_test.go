package main

import (
	"strings"
	"testing"
)

// The copy-paste this exists to catch: GitHub's list pasted onto a Forgejo hook. The forge
// accepts the request, the hook looks right in the UI, and it never fires on those events.
//
// proven-by: TestCheckEvents_RefusesGitHubsNamesOnForgejo
func TestCheckEvents_RefusesGitHubsNamesOnForgejo(t *testing.T) {
	err := checkEvents(KindGitea, []string{"push", "pull_request_review", "release"})
	if err == nil {
		t.Fatal("GitHub's event names were accepted for Forgejo")
	}
	for _, want := range []string{"pull_request_review", "release"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}

// ...and the control: the same call with Forgejo's own names passes, so the check cannot be
// satisfied only by refusing everything.
func TestCheckEvents_AcceptsForgejosOwnNames(t *testing.T) {
	if err := checkEvents(KindGitea, giteaEvents); err != nil {
		t.Fatalf("Forgejo's own vocabulary was refused: %v", err)
	}
	if err := checkEvents(KindGitHub, gitHubEvents); err != nil {
		t.Fatalf("GitHub's own vocabulary was refused: %v", err)
	}
}

// proven-by: TestForeignEvents_NamesTheCopyPastedOnes
func TestForeignEvents_NamesTheCopyPastedOnes(t *testing.T) {
	unknown, pasted := foreignEvents(KindGitea, []string{"push", "release", "nonsense"})
	if len(pasted) != 1 || pasted[0] != "release" {
		t.Errorf("pasted = %v, want [release]", pasted)
	}
	if len(unknown) != 1 || unknown[0] != "nonsense" {
		t.Errorf("unknown = %v, want [nonsense]", unknown)
	}
}

// A name neither forge has is still refused, and the message lists what is available so the
// fix is in the error rather than in a doc.
func TestCheckEvents_RefusesANameNeitherForgeHas(t *testing.T) {
	err := checkEvents(KindGitHub, []string{"push", "pull_request_sync"})
	if err == nil {
		t.Fatal("a Forgejo-only name was accepted for GitHub")
	}
	if !strings.Contains(err.Error(), "pull_request_sync") {
		t.Errorf("the error does not name the field: %v", err)
	}
}

// The vocabularies are the ones measured on the estate, not guesses: Forgejo's is the 18-event
// set a live it-test/lh-test hook reports.
func TestVocabularies_MatchWhatTheForgesReport(t *testing.T) {
	if len(gitHubEvents) != 11 {
		t.Errorf("GitHub vocabulary has %d events, want the 11 the estate's repos carry", len(gitHubEvents))
	}
	if len(giteaEvents) != 18 {
		t.Errorf("Forgejo vocabulary has %d events, want the 18 a live hook reports", len(giteaEvents))
	}
	for _, e := range []string{"pull_request_sync", "issue_assign"} {
		if !contains(giteaEvents, e) {
			t.Errorf("Forgejo vocabulary is missing %q, which a live hook carries", e)
		}
	}
	for _, e := range []string{"release", "deployment", "pull_request_review"} {
		if contains(giteaEvents, e) {
			t.Errorf("Forgejo vocabulary claims %q, which a live hook does not carry", e)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
