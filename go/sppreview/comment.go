package main

import (
	"fmt"
	"os"
	"strings"
)

// The preview's comment body is composed HERE, not in the task's shell.
//
// Two catalog guards say so from different directions: tasklint payload requires every prpost call to
// carry -body-file (a defaulted body looks complete and is not), and tasklint message refuses a step
// that grows the number of prose lines it composes in shell — because prose written in shell has
// nowhere to put a test. A Go reporter has both: the body is a value with tests, and the task passes
// the file it wrote.
const (
	previewSummaryUp   = "Preview up"
	previewSummaryDown = "the preview did not deploy"
	previewLogLines    = 40
)

// previewSummary is the one-line headline for prpost's -summary. It names the URL, because the URL is
// the thing a reviewer came to the comment for.
//
// proven-by: TestPreviewSummary_NamesTheURLWhenItIsUp
func previewSummary(url string) string {
	if url == "" {
		return previewSummaryDown
	}
	return previewSummaryUp + ": " + url
}

// previewComment is the markdown body, for the two outcomes the deploy has.
//
// A preview that did not come up is the case the comment exists for: jx failed this step on the URL of
// a preview that WAS up, so the failure has to carry the deploy's own words rather than a sentence
// about the URL.
//
// proven-by: TestPreviewComment_NamesTheURLAndTheNamespaceWhenItIsUp
// proven-by: TestPreviewComment_QuotesTheLogWhenTheDeployFailed
func previewComment(rc int, url, namespace, pullNumber, log string) string {
	var b strings.Builder
	if rc == 0 {
		b.WriteString("| | |\n|---|---|\n")
		fmt.Fprintf(&b, "| preview | %s |\n", url)
		fmt.Fprintf(&b, "| namespace | `%s` |\n", namespace)
		fmt.Fprintf(&b, "| pull request | #%s |\n", pullNumber)
		return b.String()
	}
	fmt.Fprintf(&b, "The preview step exited %d. Its output:\n\n", rc)
	b.WriteString("```\n")
	b.WriteString(tailLines(log, previewLogLines))
	b.WriteString("```\n")
	return b.String()
}

// writePreviewComment writes the body and the summary where the task can hand them to prpost, and
// prints the summary so the step's own log says what was posted.
//
// proven-by: TestWritePreviewComment_WritesBothFiles
func writePreviewComment(rc int, url, namespace, pullNumber, logPath, bodyPath, summaryPath string) error {
	// A failed deploy is exactly when the log matters, so an unreadable log is not a reason to post
	// nothing — it is a reason to post a body that says the log was missing.
	log := readTail(logPath)
	body := previewComment(rc, url, namespace, pullNumber, log)
	summary := previewSummary(url)

	if err := os.WriteFile(bodyPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing the preview comment body: %w", err)
	}
	if summaryPath != "" {
		if err := os.WriteFile(summaryPath, []byte(summary), 0o644); err != nil {
			return fmt.Errorf("writing the preview summary: %w", err)
		}
	}
	fmt.Println(summary)
	return nil
}

func readTail(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "the step's log could not be read: " + err.Error()
	}
	return string(body)
}

// tailLines keeps the end of a log, which is where a helmfile or a kubectl failure says what happened.
//
// proven-by: TestTailLines_KeepsTheEndAndSaysHowMuchItDropped
func tailLines(body string, n int) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n") + "\n"
	}
	dropped := len(lines) - n
	kept := lines[dropped:]
	return fmt.Sprintf("... %d earlier line(s)\n%s\n", dropped, strings.Join(kept, "\n"))
}
