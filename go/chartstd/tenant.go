package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// S10 — a released chart must not carry a TENANT-SPECIFIC default.
//
// WHY. leartech-ship-proven-umbrella 0.0.130 shipped two values to every
// customer:
//
//	serviceAccount.annotations["iam.gke.io/gcp-service-account"]:
//	    console-api-gsm-writer@product-first.iam.gserviceaccount.com
//	suppliers.gsmProject: product-first
//
// S6 forbids the annotation in a chart and would have caught it — S6 had never
// run on this repo, because the umbrella's PR pipeline is tasks/helm, which did
// not invoke chartstd. That half is fixed by wiring, not by a rule. The other
// half is this rule: nothing covered `gsmProject`, which is a fact about ONE
// customer's GCP project written into a chart every customer installs.
//
// PRECISION MATTERS MORE THAN REACH here. The estate legitimately shares an
// enormous amount with every customer — `mikelear/*` repo refs, our own
// `*.jx.leartech.com` hostnames, our GSM project names in `remoteRef` keys that
// point at OUR backend. A rule that flags those is the rule that gets switched
// off. So S10 fires on exactly two shapes, both of which cannot be right for
// every customer:
//
//	1. a GSA email — `…@<project>.iam.gserviceaccount.com`. The GSA belongs to
//	   one project; a customer's cluster has its own.
//	2. a value key whose NAME says "which project" (`gsmProject`, `gcpProject`,
//	   `projectId`, `project_id`, `gcpProjectId`) holding a literal. The key name
//	   is the claim; a literal answers it for one tenant only.
//
// Deliberately NOT flagged: hostnames, repository paths, secret NAMES, and any
// value that is empty, templated, or a placeholder — all of those are shared
// estate furniture, not tenant identity.

var gsaEmail = regexp.MustCompile(`[A-Za-z0-9._%-]+@[A-Za-z0-9-]+\.iam\.gserviceaccount\.com`)

// projectKey: the key names that ask "which project is this customer's".
var projectKey = regexp.MustCompile(`(?i)^(gsmProject|gcpProject|gcpProjectId|projectId|project_id)$`)

// notATenant: a value that is a reference, empty, templated, or explicitly a
// placeholder. None of these name anybody.
var notATenant = regexp.MustCompile(`(?i)^(\{\{|""|''|~|null|todo|changeme|example|placeholder|your-|<)`)

func tenantFindings(chartDir string) []Finding {
	path := filepath.Join(chartDir, "values.yaml")
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the chart
	if err != nil {
		return nil
	}

	var vals map[string]any
	if err := yaml.Unmarshal(b, &vals); err != nil {
		return []Finding{{"S10", path, fmt.Sprintf(
			"cannot be parsed (%v), so its defaults cannot be judged", err)}}
	}

	var out []Finding
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			v := m[k]
			path2 := k
			if prefix != "" {
				path2 = prefix + "." + k
			}
			switch val := v.(type) {
			case map[string]any:
				walk(path2, val)
			case string:
				// An empty value is the CORRECT form: the deploy supplies the
				// project. YAML has already unquoted `""`, so the regex cannot
				// see it -- caught by the tenant-clean fixture, which failed.
				if strings.TrimSpace(val) == "" ||
					notATenant.MatchString(strings.TrimSpace(val)) {
					continue
				}
				if m := gsaEmail.FindString(val); m != "" {
					out = append(out, Finding{"S10", path, fmt.Sprintf(
						"%s = %q names a Google service account in ONE project. This chart is "+
							"installed by every customer; their cluster has its own GSA, and "+
							"this one does not exist there — the failure is a provider error "+
							"that names neither the annotation nor the chart. Take it from "+
							"the deploy (terraform output), not from a released default.",
						path2, m)})
					continue
				}
				if projectKey.MatchString(k) {
					out = append(out, Finding{"S10", path, fmt.Sprintf(
						"%s = %q answers \"which GCP project\" for one tenant. Every customer "+
							"has a different project, so a released default is wrong for all "+
							"but the one it was written for — and wrong SILENTLY: the chart "+
							"installs, and the value reaches a backend that does not exist.",
						path2, val)})
				}
			case []any:
				for i, item := range val {
					if s, ok := item.(string); ok && !notATenant.MatchString(strings.TrimSpace(s)) {
						if m := gsaEmail.FindString(s); m != "" {
							out = append(out, Finding{"S10", path, fmt.Sprintf(
								"%s[%d] = %q names a Google service account in ONE project.",
								path2, i, m)})
						}
					}
				}
			}
		}
	}
	walk("", vals)
	return out
}
