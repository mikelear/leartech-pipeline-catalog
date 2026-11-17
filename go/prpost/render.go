package main

import (
	"fmt"
	"sort"
	"strings"
)

// The human half of a comment, rendered in Go from typed facts.
//
// WHY THIS MOVED OUT OF SHELL. Every report a step posts is two payloads: markdown for a
// person, and the `leartech-report` block for an agent. prpost already rendered the machine
// half from its own flags — but the MARKDOWN was assembled by the caller, in shell, with
// `printf` and `echo`:
//
//	printf '### :%s: Coverage: %s%% (threshold %s%%) `[%s]` — %s\n\n' \
//	  "$ICON" "$TOTAL" "$THRESHOLD" "$CLUSTER_TAG" "$VERDICT"
//
// That prose has no test, because shell has nowhere to put one — and it is where every
// misreported verdict this estate has had came from: "0.0% (threshold 60%)" for a scope that
// measured nothing, "Delta vs main: unavailable" permanently, "CROSS-SERVICE AUDIENCE
// BINDING BROKEN" about a 404'd route, "2/5 checks passed, 16 skipped" when the runner handed
// the pack nothing. Measured at the time: 63 markdown lines and 345 interpolated echo/printf
// lines across 34 task files, and not one assertion over any of them.
//
// Rendering here makes the prose a function of typed input, which makes it a golden test: the
// same facts must produce byte-identical markdown, and changing a sentence is a reviewable
// diff instead of an invisible edit to a `printf`.
//
// The renderer does NOT decide the verdict. `-outcome` is the caller's, because only the step
// knows whether its test command passed; what moves here is the wording of that verdict and
// the table around it. That boundary is deliberate: a renderer that guessed would be a second
// opinion about pass/fail, which is the class of bug this whole exercise is about.

// Facts are the tool-specific values a renderer interpolates. Keys are documented per tool
// below; an absent key renders its placeholder, never an empty string, so a missing fact is
// visible in the comment rather than silently blank.
type Facts map[string]string

// Test is one row of a report table.
type Test struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // pass | fail | skip
	Duration string `json:"duration,omitempty"`
	Message  string `json:"message,omitempty"`
}

// pct renders a percentage-shaped fact with its unit. The step computes `50.0`; the comment must
// say `50.0%`. Adding the unit in the renderer means a caller cannot forget it, and it is why
// the first real posting read "Coverage: 50.0 (threshold 0.0)".
func pct(v string) string {
	if v == "" || v == "?" || strings.HasSuffix(v, "%") {
		return v
	}
	return v + "%"
}

func (f Facts) get(key, missing string) string {
	if v, ok := f[key]; ok && v != "" {
		return v
	}
	return missing
}

// RenderVisual dispatches to the tool's renderer. An unknown tool gets the generic shape, so
// a new step posts something truthful rather than nothing — and the golden tests name every
// tool that has a dedicated one, so adding a tool without adding a fixture is visible.
func RenderVisual(tool string, rep Report, f Facts, tests []Test, doc string) string {
	switch tool {
	case "coverage":
		return renderCoverage(rep, f)
	case "end2end":
		return renderEnd2end(rep, f, tests, doc, "End-to-end")
	case "end2end-ui":
		return renderEnd2end(rep, f, tests, doc, "End-to-end UI")
	case "mutants":
		return renderMutants(rep, f)
	case "dynamic-scan":
		return renderDynamicScan(rep, f)
	}
	return renderGeneric(rep, f, tests, doc)
}

// icon maps an outcome to the emoji the estate already uses, so a reader's eye works the same
// across every check.
func icon(outcome string) string {
	switch strings.ToLower(outcome) {
	case "success":
		return ":white_check_mark:"
	case "failure":
		return ":x:"
	case "pending":
		return ":hourglass_flowing_sand:"
	}
	return ":grey_question:"
}

// verdictWord is the human word for the outcome, used where a table cell or a heading needs
// one. It falls back to the raw outcome rather than guessing.
func verdictWord(outcome string) string {
	switch strings.ToLower(outcome) {
	case "success":
		return "PASS"
	case "failure":
		return "FAIL"
	case "pending":
		return "RUNNING"
	}
	return strings.ToUpper(outcome)
}

// renderCoverage is the go/ng/rust/dotnet test report: the number, the floor, the delta
// against the base branch, and the weakest functions.
//
// Facts: threshold, base, delta, scope, tolerance, delta_status (OK | REGRESSED |
// UNAVAILABLE), delta_reason, lowest (a ready-made markdown table body).
func renderCoverage(rep Report, f Facts) string {
	var b strings.Builder
	// A percentage that came from an empty profile is not a measurement. Printing "0.0%" for it
	// is how a scope mismatch spent a year looking like untested code, so the headline says what
	// happened instead of showing a number nobody earned.
	headline := "Coverage: " + pct(f.get("coverage", "?"))
	if f.get("measured", "") == "nothing" {
		headline = "Coverage: NOT MEASURED"
	}
	fmt.Fprintf(&b, "### %s %s (threshold %s) `[%s]` — %s\n\n",
		icon(rep.Outcome), headline, pct(f.get("threshold", "?")), rep.Cluster,
		f.get("verdict", verdictWord(rep.Outcome)))

	base := pct(f.get("base", "?"))
	switch f.get("delta_status", "") {
	case "UNAVAILABLE":
		// The reason matters more than the absence. "unavailable" with no why is what made a
		// permanently-broken baseline clone read as a transient network blip for a whole forge.
		fmt.Fprintf(&b, "_Delta vs `%s`: unavailable — %s_\n\n",
			f.get("base_ref", "main"), f.get("delta_reason", "reason not reported"))
	case "REGRESSED":
		fmt.Fprintf(&b, ":warning: **Delta vs `%s`**: %s → %s (`%s` — exceeds tolerance `-%s`)\n\n",
			f.get("base_ref", "main"), base, pct(f.get("coverage", "?")),
			pct(f.get("delta", "?")), pct(f.get("tolerance", "?")))
	default:
		if f.get("delta", "") == "" {
			fmt.Fprintf(&b, ":information_source: Delta vs `%s`: unavailable — %s\n\n",
				f.get("base_ref", "main"), f.get("delta_reason", "reason not reported"))
			break
		}
		fmt.Fprintf(&b, ":information_source: Delta vs `%s`: %s → %s (`%s`)\n\n",
			f.get("base_ref", "main"), base, pct(f.get("coverage", "?")), pct(f.get("delta", "?")))
	}

	b.WriteString("**Lowest-covered functions**\n\n")
	b.WriteString("| File | Function | Coverage |\n|---|---|---|\n")
	b.WriteString(strings.TrimRight(f.get("lowest", "| — | — | all functions ≥ 100% |"), "\n"))
	b.WriteString("\n")
	return b.String()
}

// renderEnd2end is the checklist report: how many checks ran, which failed, and the doc
// fragment the suite itself wrote.
//
// Facts: counts (e.g. "4/4 checks passed"), target (the preview URL), artifacts
// (markdown links), logs (a fenced tail of output).
func renderEnd2end(rep Report, f Facts, tests []Test, doc, title string) string {
	var b strings.Builder
	head := fmt.Sprintf("%s **%s: %s** `[%s]`", icon(rep.Outcome), title,
		verdictWord(rep.Outcome), rep.Cluster)
	if f.get("counts", "") != "" {
		head += " — " + f.get("counts", "")
	}
	b.WriteString(head + "\n")
	if t := f.get("target", ""); t != "" {
		b.WriteString("\nPreview: " + t + "\n")
	}
	if len(tests) > 0 {
		b.WriteString("\n| Test | Status | Duration | Message |\n|---|---|---|---|\n")
		for _, t := range tests {
			fmt.Fprintf(&b, "| `%s` | %s %s | %s | %s |\n",
				t.Name, testIcon(t.Status), t.Status,
				orDash(t.Duration), oneLine(t.Message))
		}
	}
	if a := strings.TrimSpace(f.get("artifacts", "")); a != "" {
		b.WriteString("\n**Artifacts** (screenshots, videos, traces):\n\n")
		b.WriteString(a)
		b.WriteString("\n")
	}
	if len(rep.Failures) > 0 {
		b.WriteString("\n")
		for _, fl := range rep.Failures {
			fmt.Fprintf(&b, "- %s\n", oneLine(fl))
		}
	}
	// The log tail goes in a fence above the doc block, unflattened: the lines are the point.
	if lg := strings.TrimRight(f.get("logs", ""), "\n"); lg != "" {
		b.WriteString("\n```\n" + lg + "\n```\n")
	}
	if doc = strings.TrimSpace(doc); doc != "" {
		b.WriteString("\n<details><summary>What this run measured</summary>\n\n")
		b.WriteString(doc)
		b.WriteString("\n\n</details>\n")
	}
	return b.String()
}

// renderMutants is the Stryker report: outcomes as counts, and the mutations that survived —
// the useful half, because a score alone says nothing about which gap to close.
//
// Facts: score, break_at, killed, survived, nocoverage, timeouts, survivors (table body).
func renderMutants(rep Report, f Facts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s Stryker.NET `[%s]` — %s (%s)\n\n",
		icon(rep.Outcome), rep.Cluster, pct(f.get("score", "?")), f.get("verdict", verdictWord(rep.Outcome)))
	fmt.Fprintf(&b, "**Outcomes**: killed=`%s` survived=`%s` no-coverage=`%s` timeout=`%s` (score %s)\n\n",
		f.get("killed", "?"), f.get("survived", "?"), f.get("nocoverage", "?"),
		f.get("timeouts", "?"), pct(f.get("score", "?")))
	b.WriteString("**Top surviving mutations** (gaps in test coverage):\n\n")
	b.WriteString("| File | Line | Mutator |\n|---|---|---|\n")
	b.WriteString(strings.TrimRight(f.get("survivors", "| — | — | no survivors |"), "\n"))
	b.WriteString("\n")
	return b.String()
}

// renderDynamicScan reports a scan that may never have run, and says which — the distinction
// the whole estate keeps losing: "skipped because the preview was not ready" is not
// "no vulnerabilities found", and only one of them is good news.
//
// Facts: state (skipped | running | complete), why, scanners.
func renderDynamicScan(rep Report, f Facts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s **Dynamic security scan: %s** `[%s]`\n\n",
		icon(rep.Outcome), verdictWord(rep.Outcome), rep.Cluster)
	if w := f.get("why", ""); w != "" {
		b.WriteString(w + "\n")
	}
	if s := f.get("scanners", ""); s != "" {
		b.WriteString("\n" + s + "\n")
	}
	return b.String()
}

// renderGeneric is what a tool without a dedicated renderer posts: the summary, the failures,
// and any detail. Deliberately plain — a new step gets a truthful comment before it gets a
// designed one, and the golden tests make "this tool has no renderer" visible rather than
// silent.
func renderGeneric(rep Report, f Facts, tests []Test, doc string) string {
	var b strings.Builder
	// The tool is named, because a fallback comment has no other way to say who spoke: a
	// reader cannot act on "3 of 5 sub-checks failed" without knowing which check.
	fmt.Fprintf(&b, "%s **%s**: %s `[%s]`\n", icon(rep.Outcome), rep.Tool, rep.Summary, rep.Cluster)
	if len(rep.Failures) > 0 {
		b.WriteString("\n")
		for _, fl := range rep.Failures {
			fmt.Fprintf(&b, "- %s\n", oneLine(fl))
		}
	}
	if len(tests) > 0 {
		b.WriteString("\n| Test | Status | Duration | Message |\n|---|---|---|---|\n")
		for _, t := range tests {
			fmt.Fprintf(&b, "| `%s` | %s %s | %s | %s |\n",
				t.Name, testIcon(t.Status), t.Status, orDash(t.Duration), oneLine(t.Message))
		}
	}
	if doc = strings.TrimSpace(doc); doc != "" {
		b.WriteString("\n" + doc + "\n")
	}
	return b.String()
}

func testIcon(status string) string {
	switch strings.ToLower(status) {
	case "pass":
		return ":white_check_mark:"
	case "fail":
		return ":x:"
	case "skip":
		return ":large_blue_circle:"
	}
	return ":grey_question:"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// oneLine flattens a message into a table cell: pipes escaped, newlines joined, length capped.
// A raw newline in a markdown table breaks the table and the reader sees one row of nonsense.
// collapse makes any value one line with single spaces between words.
//
// A suite emits folded, indented messages ("This check is       staging-only: ...") and that
// indentation survives into results.json. This flattens its table cells; the machine block
// carried the raw string, so the two halves of one comment disagreed about the same value.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(collapse(s), "|", "\\|")
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

// SortedKeys is used by the render CLI to print the facts it understood, which is how a
// consumer debugs "why does my comment say ?".
func SortedKeys(f Facts) []string {
	out := make([]string, 0, len(f))
	for k := range f {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
