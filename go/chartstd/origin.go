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

// S11 and S12 — an origin allow-list in a released chart.
//
// WHY THIS IS NOT S10. S10 deliberately does not flag hostnames: our own FQDNs are
// estate furniture, shared by every customer's install (`*.jx.leartech.com` appears in
// ingress defaults, `mcp.host` names OUR backend, and a customer override replaces
// them). An origin allow-list is the opposite kind of value. It is an ADMISSION
// POLICY, not a pointer: it decides which browser pages may talk to an authenticated
// API, so a wrong entry does not fail — it either admits someone or blocks the real UI.
//
// TWO SHAPES, AND THEY FAIL IN OPPOSITE DIRECTIONS.
//
//	S11  a WILDCARD (`*`, `https://*`, `*.example.com`). Fails OPEN: every origin is
//	     admitted. `Access-Control-Allow-Origin: *` is additionally invalid with
//	     credentials, so the browser refuses the combination the app relies on and the
//	     symptom appears as "the portal gets nothing back" rather than "CORS is wrong".
//	     The same mistake in a WebSocket check is worse: an allow-list that accepts any
//	     origin lets another site open an authenticated socket.
//
//	S12  a LITERAL origin (`https://portal.example.com`) defaulted in the chart. Fails
//	     CLOSED for the deploy that inherits it: a preview of the same chart serves
//	     pr47.jx.…, a customer serves their own host, so the default matches NEITHER and
//	     every request is blocked by the browser while the pod is perfectly healthy. A
//	     chart cannot know its own origin — it should take it from the deploy, or derive
//	     it from jxRequirements.
//
// NO ESTATE CHART FIRES THIS TODAY: console-api and artifact-api both default these
// lists EMPTY and `required` them in the template, which is the correct form and is
// what the fixtures pin.
//
// proven-by: TestOriginAllowListIsNotDefaulted
func originFindings(chartDir string) []Finding {
	path := filepath.Join(chartDir, "values.yaml")
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the chart
	if err != nil {
		// Not this rule's finding: S10 already refuses to pass a chart whose values
		// cannot be read, and two rules reporting one unreadable file is noise.
		return nil
	}

	var vals map[string]any
	if err := yaml.Unmarshal(b, &vals); err != nil {
		return nil
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
			path2 := k
			if prefix != "" {
				path2 = prefix + "." + k
			}
			switch val := m[k].(type) {
			case map[string]any:
				walk(path2, val)
			case string:
				if !originKey.MatchString(k) {
					continue
				}
				out = append(out, judgeOrigin(path, path2, val)...)
			case []any:
				// A list under `cors` itself (`cors: [https://…]`) is the same policy
				// with a different spelling; a list under any other key is only this
				// rule's business when the key says origin.
				if !originKey.MatchString(k) && !corsListKey.MatchString(k) {
					continue
				}
				for i, item := range val {
					s, ok := item.(string)
					if !ok {
						continue
					}
					out = append(out, judgeOrigin(path, fmt.Sprintf("%s[%d]", path2, i), s)...)
				}
			}
		}
	}
	walk("", vals)
	return out
}

// originKey is the key names that hold an origin allow-list. `origin` covers
// `origins`, `allowedOrigins`, `allowOrigins`, `trustedOrigins`, `corsOrigins`,
// `originPatterns` and `wsOriginPatterns` — the spellings in this estate.
var originKey = regexp.MustCompile(`(?i)origin`)

// corsListKey is the other spelling: the allow-list IS the `cors` value.
var corsListKey = regexp.MustCompile(`(?i)^cors$`)

// schemeOrigin is a browser origin: a scheme, a host, and no path.
var schemeOrigin = regexp.MustCompile(`(?i)^https?://[a-z0-9._:\[\]-]+/?$`)

// judgeOrigin classifies one entry. Empty, templated and placeholder values are the
// correct form here — the deploy supplies the origin — so they are silent, exactly as
// S10 treats an empty project.
func judgeOrigin(path, where, raw string) []Finding {
	v := strings.TrimSpace(strings.Trim(raw, `"'`))
	if v == "" || notATenant.MatchString(v) {
		return nil
	}
	if strings.Contains(v, "*") {
		return []Finding{{"S11", path, fmt.Sprintf(
			"%s = %q is a WILDCARD in an origin allow-list, so it admits every origin. "+
				"That is a policy decision no chart can make for its installs, and "+
				"`Access-Control-Allow-Origin: *` is invalid with credentials — the browser "+
				"refuses the pair the app relies on, and the failure reads as a backend "+
				"returning nothing. Name the deploy's own origin, or default the list empty "+
				"and `required` it in the template.", where, v)}}
	}
	if schemeOrigin.MatchString(v) {
		return []Finding{{"S12", path, fmt.Sprintf(
			"%s = %q is a LITERAL origin defaulted in a released chart. The same chart "+
				"serves a preview at pr<N>.… and a customer at their own host, so this "+
				"value matches NEITHER: the pod is healthy and the browser blocks every "+
				"request. Default the list empty and let the deploy supply the origin.", where, v)}}
	}
	return nil
}
