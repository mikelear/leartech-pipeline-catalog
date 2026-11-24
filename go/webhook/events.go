package main

import (
	"fmt"
	"sort"
	"strings"
)

// The two forges have different event vocabularies, and the difference is silent: a hook sent
// events the forge does not know is still created, and simply does not fire on them.
//
// Measured, not assumed. These are the two sets as the forges actually report them:
//
//   - GitHub (11): the set jx's own lighthouse enrolment used, and what every GitHub repo in
//     this estate carries.
//   - Forgejo (18): read back from it-test/lh-test's live hook, the repo the pipeline canary
//     exercises every two hours. It has no `deployment`, `deployment_status`, `release` or
//     bare `pull_request_review`, and it has Forgejo-only names (`pull_request_sync`,
//     `issue_assign`, …) that GitHub rejects.
//
// So one shared list in a config file is wrong for one of them, and a copy-paste between the
// two entries produces a hook that looks correct and misses events.
var (
	// gitHubEvents is GitHub's hook event vocabulary.
	gitHubEvents = []string{
		"create", "delete", "deployment", "deployment_status", "issues", "issue_comment",
		"pull_request", "pull_request_review", "pull_request_review_comment", "push", "release",
	}
	// giteaEvents is Gitea/Forgejo's, as a live Forgejo hook reports it.
	giteaEvents = []string{
		"create", "delete", "issue_assign", "issue_comment", "issue_label", "issue_milestone",
		"issues", "pull_request", "pull_request_assign", "pull_request_comment",
		"pull_request_label", "pull_request_milestone", "pull_request_review_approved",
		"pull_request_review_comment", "pull_request_review_rejected",
		"pull_request_review_request", "pull_request_sync", "push",
	}
)

// eventsFor returns the vocabulary for kind.
func eventsFor(kind Kind) []string {
	if kind == KindGitHub {
		return gitHubEvents
	}
	return giteaEvents
}

// foreignEvents returns the events in want that kind does not know, and which of them the OTHER
// forge does know — that second list is the copy-paste case, and naming it is the whole point.
// proven-by: TestForeignEvents_NamesTheCopyPastedOnes
func foreignEvents(kind Kind, want []string) (unknown, pasted []string) {
	known := make(map[string]struct{}, len(eventsFor(kind)))
	for _, e := range eventsFor(kind) {
		known[e] = struct{}{}
	}
	other := eventsFor(KindGitHub)
	if kind == KindGitHub {
		other = eventsFor(KindGitea)
	}
	otherSet := make(map[string]struct{}, len(other))
	for _, e := range other {
		otherSet[e] = struct{}{}
	}
	for _, e := range want {
		if _, ok := known[e]; ok {
			continue
		}
		if _, ok := otherSet[e]; ok {
			pasted = append(pasted, e)
			continue
		}
		unknown = append(unknown, e)
	}
	sort.Strings(unknown)
	sort.Strings(pasted)
	return unknown, pasted
}

// checkEvents refuses a hook whose events this provider does not have. It is an error and not
// a warning because the forge accepts the request: the hook exists, looks right in the UI, and
// does not fire on the events that were mistyped.
// proven-by: TestCheckEvents_RefusesGitHubsNamesOnForgejo
func checkEvents(kind Kind, want []string) error {
	unknown, pasted := foreignEvents(kind, want)
	if len(pasted) > 0 {
		return fmt.Errorf("event(s) %s are the OTHER forge's names — %s knows %s",
			strings.Join(pasted, ", "), kind, strings.Join(eventsFor(kind), ", "))
	}
	if len(unknown) > 0 {
		return fmt.Errorf("event(s) %s are not %s events (known: %s)",
			strings.Join(unknown, ", "), kind, strings.Join(eventsFor(kind), ", "))
	}
	return nil
}
