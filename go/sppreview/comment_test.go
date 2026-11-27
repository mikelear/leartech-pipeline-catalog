package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The URL is what a reviewer opens the comment for, so the headline carries it rather than saying
// "success".
//
// proven-by: TestPreviewSummary_NamesTheURLWhenItIsUp
func TestPreviewSummary_NamesTheURLWhenItIsUp(t *testing.T) {
	url := "https://leartech-forge-api-pr19.jx.leartech.com"
	if got := previewSummary(url); !strings.Contains(got, url) {
		t.Errorf("previewSummary = %q, want it to name %s", got, url)
	}
	// The failure headline must NOT carry a URL: a conventional fallback URL for a preview that never
	// deployed is a link to nothing.
	if got := previewSummary(""); strings.Contains(got, "https://") {
		t.Errorf("previewSummary(\"\") = %q, want no URL", got)
	}
	if got := previewSummary(""); got != previewSummaryDown {
		t.Errorf("previewSummary(\"\") = %q, want %q", got, previewSummaryDown)
	}
}

// A deployed preview's body names the three things a reader needs to act: where it is, which namespace
// holds it, and which pull request it belongs to.
//
// proven-by: TestPreviewComment_NamesTheURLAndTheNamespaceWhenItIsUp
func TestPreviewComment_NamesTheURLAndTheNamespaceWhenItIsUp(t *testing.T) {
	body := previewComment(0, "https://app-pr19.example.com", "jx-owner-app-pr-19", "19", "ignored")

	for _, want := range []string{
		"https://app-pr19.example.com",
		"jx-owner-app-pr-19",
		"#19",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not carry %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "```") {
		t.Errorf("a successful deploy quoted a log:\n%s", body)
	}
}

// The failed case is the one that has to explain itself, and the deploy's own words are the only
// explanation worth posting.
//
// proven-by: TestPreviewComment_QuotesTheLogWhenTheDeployFailed
func TestPreviewComment_QuotesTheLogWhenTheDeployFailed(t *testing.T) {
	body := previewComment(1, "", "jx-owner-app-pr-19", "19",
		"Adding repo jx3\nerror during helmfile.yaml.gotmpl parsing: requiredEnv \"PREVIEW_NAMESPACE\" is not set\n")

	if !strings.Contains(body, "exited 1") {
		t.Errorf("the body does not name the exit code:\n%s", body)
	}
	if !strings.Contains(body, `requiredEnv "PREVIEW_NAMESPACE" is not set`) {
		t.Errorf("the body does not quote the log:\n%s", body)
	}
	if strings.Count(body, "```") != 2 {
		t.Errorf("the log is not in exactly one fence:\n%s", body)
	}
}

// Both files are what the task passes to prpost, so a run that writes one and not the other posts a
// defaulted field — the payload guard's whole complaint.
//
// proven-by: TestWritePreviewComment_WritesBothFiles
func TestWritePreviewComment_WritesBothFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "step.log")
	if err := os.WriteFile(logPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bodyPath := filepath.Join(dir, "body.md")
	summaryPath := filepath.Join(dir, "summary.txt")

	if err := writePreviewComment(0, "https://app-pr19.example.com", "jx-o-app-pr-19", "19",
		logPath, bodyPath, summaryPath); err != nil {
		t.Fatalf("writePreviewComment: %v", err)
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil || !strings.Contains(string(body), "https://app-pr19.example.com") {
		t.Errorf("body = %q, err %v", body, err)
	}
	summary, err := os.ReadFile(summaryPath)
	if err != nil || !strings.Contains(string(summary), "https://app-pr19.example.com") {
		t.Errorf("summary = %q, err %v", summary, err)
	}
}

// An unreadable log must still produce a comment. A failed deploy that posts nothing is the defect a
// comment on a red check exists to fix.
func TestWritePreviewComment_SurvivesAMissingLog(t *testing.T) {
	dir := t.TempDir()
	bodyPath := filepath.Join(dir, "body.md")

	if err := writePreviewComment(1, "", "jx-o-app-pr-19", "19",
		filepath.Join(dir, "no-such.log"), bodyPath, ""); err != nil {
		t.Fatalf("writePreviewComment: %v", err)
	}
	body, err := os.ReadFile(bodyPath)
	if err != nil || !strings.Contains(string(body), "could not be read") {
		t.Errorf("body = %q, err %v", body, err)
	}
}

// The tail is where a helmfile or a kubectl failure says what happened, and a comment is not a log
// viewer: the dropped count keeps the quote honest about what it is not showing.
//
// proven-by: TestTailLines_KeepsTheEndAndSaysHowMuchItDropped
func TestTailLines_KeepsTheEndAndSaysHowMuchItDropped(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	got := tailLines(b.String(), 3)

	for _, want := range []string{"line 8", "line 9", "line 10"} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("the tail did not keep %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line 7") {
		t.Errorf("the tail kept a line it said it dropped:\n%s", got)
	}
	if !strings.Contains(got, "7 earlier line(s)") {
		t.Errorf("the tail does not say how much it dropped:\n%s", got)
	}
}

// A short log is quoted whole, with no count, because "0 earlier line(s)" is noise.
func TestTailLines_QuotesAShortLogWhole(t *testing.T) {
	got := tailLines("only one\n", 40)
	if strings.Contains(got, "earlier line") || !strings.Contains(got, "only one") {
		t.Errorf("tailLines = %q", got)
	}
}
