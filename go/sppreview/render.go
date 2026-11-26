package main

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// cleanRender keeps only the YAML documents from a `helmfile template` stream.
//
// helmfile writes its progress into the SAME stream as the manifests:
//
//	Adding repo jx3 https://jenkins-x-charts.github.io/repo
//	Templating release=jx-verify, chart=jx3/jx-verify
//	---
//	# Source: jx-verify/templates/job-sa.yaml
//	kind: ServiceAccount
//
// and the consumer of a render is the chart-standard gate, which refuses the whole file on
// `cannot unmarshal !!str "Adding ..." into map`. So without this, a preview can never be checked
// by the code that already checks the chart.
//
// TRIMMING, NOT PREFIX-MATCHING. The chatter lands both BEFORE the first document and AFTER a
// document (the "Templating release=…" line for the next release follows the previous manifest), so
// dropping whole documents loses real ones — measured: 33 documents became 30. Each document is
// therefore trimmed from its edges until it parses as a mapping, and a document that never parses is
// chatter and is dropped.
// proven-by: TestCleanRender_KeepsADocumentThatChatterFollows
func cleanRender(out string) string {
	var kept []string
	for _, doc := range splitDocs(out) {
		if cleaned, ok := trimToMapping(doc); ok {
			kept = append(kept, "---\n"+cleaned)
		}
	}
	return strings.Join(kept, "")
}

// trimToMapping removes edge lines until the remainder parses as a YAML mapping. Leading lines are
// tried first (chatter before a document) and then trailing ones (chatter after it), so the manifest
// itself is never touched.
func trimToMapping(doc string) (string, bool) {
	lines := strings.Split(strings.Trim(doc, "\n"), "\n")
	if parsesAsMapping(lines) {
		return strings.Join(lines, "\n") + "\n", true
	}
	for drop := 1; drop <= len(lines); drop++ {
		if rest := lines[drop:]; parsesAsMapping(rest) {
			return strings.Join(rest, "\n") + "\n", true
		}
		if rest := lines[:len(lines)-drop]; parsesAsMapping(rest) {
			return strings.Join(rest, "\n") + "\n", true
		}
	}
	return "", false
}

func parsesAsMapping(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	var probe any
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "\n")), &probe); err != nil {
		return false
	}
	_, ok := probe.(map[string]any)
	return ok
}

// splitDocs splits a helmfile stream on its own `---` separators.
func splitDocs(out string) []string {
	var docs []string
	var cur strings.Builder
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "---") {
			if cur.Len() > 0 {
				docs = append(docs, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	if cur.Len() > 0 {
		docs = append(docs, cur.String())
	}
	return docs
}
