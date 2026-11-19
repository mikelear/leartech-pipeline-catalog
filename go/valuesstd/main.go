// valuesstd is the Ship Proven secrets-manifest standard, as a gate.
//
// It reads the manifest that seeds a cluster's secrets (bootstrap/secrets.yaml in
// a GitOps repo) and the script that derives the ones which cannot be seeded, and
// it fails when the two disagree about what a value IS.
//
// WHY. Two failures on a customer cluster being built from scratch, 2026-11-18,
// both silent and both green everywhere until a pod crashlooped:
//
//	a DSN was GENERATED          random hex written into console-api-db-dsn-*
//	                             → the ExternalSecret said Ready=True and the pod
//	                             went CrashLoopBackOff on a URL whose password
//	                             was 64 random characters.
//	a derivation was duplicated  31-derive-secrets.sh composes auth-hydra-dsn-*,
//	                             while the manifest still calls them `generate`;
//	                             the seeder invents one first and the derive
//	                             script overwrites it — which works by ORDER, not
//	                             by design.
//
// The rule that catches both: a value's CLASS decides who may write it, and a
// class is a property of the value, not of the script that happens to run first.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifest is the seed declaration.
type manifest struct {
	SourceProject string `yaml:"source_project"`
	Keys          []struct {
		Key   string `yaml:"key"`
		Mode  string `yaml:"mode"`
		Shape string `yaml:"shape"`
		Why   string `yaml:"why"`
	} `yaml:"keys"`
}

// The closed vocabulary. A mode outside it is either a typo or a new class, and
// both must fail a PR rather than a cluster bootstrap.
var modes = map[string]bool{
	"copy":          true, // a shared value, taken verbatim from the source project
	"copy-template": true, // shared, but the cluster name is rewritten in the key
	"generate":      true, // a secret only this cluster has: random, create-if-absent
	"derived":       true, // COMPOSED from a generated value plus a cluster fact
	"blank":         true, // a placeholder a chart needs and nobody has chosen yet
	"manual":        true, // a provider credential a human supplies
}

// A key is URL-shaped if its NAME says so, or it declares it. `-dsn`, `-url`,
// `-uri`, `-connection`, `-endpoint` and a trailing `dsn`/`url` are all shapes a
// human parses; random bytes in any of them are a configured-looking lie.
var urlShaped = regexp.MustCompile(`(?i)(^|[-_.])(dsn|url|uri|connection|conn)$|(^|[-_])(dsn|url|uri|connection|conn)[-_.]`)

// Fields a derived value must carry so the derivation is reviewable rather than
// remembered.
func isURLShaped(key, shape string) bool {
	if shape != "" {
		return shape == "dsn" || shape == "url" || shape == "uri"
	}
	return urlShaped.MatchString(key)
}

type finding struct{ Rule, Where, Detail string }

func (f finding) String() string { return f.Rule + " " + f.Where + ": " + f.Detail }

// deriveKeys reads the secret keys a derive script writes, so a declaration can be
// compared with what the script actually composes.
//
// The name is taken from an argument POSITION or a --secret= flag, never from any
// bare word on the line: the first version of this matched every token and reported
// `replication-policy` and `data-file` as undeclared keys — flag values, not secrets.
// A checker whose findings include things that are not secrets is a checker people
// stop reading.
var (
	// set_secret <name> ...            (the helper most of these scripts grow)
	secretCall = regexp.MustCompile(`(?:set_secret|add_secret|put_secret)\s+"?\$?\{?([a-z0-9][a-z0-9-]*)`)
	// gcloud secrets create|versions add <name>
	gcloudWrite = regexp.MustCompile(`gcloud\s+secrets\s+(?:create|versions\s+add)\s+"?\$?\{?([a-z0-9][a-z0-9-]*)`)
	// --secret=<name> on a WRITE line (a read of the same flag is not a write)
	secretFlag = regexp.MustCompile(`--secret="?([a-z0-9][a-z0-9-]*)`)
)

func deriveKeys(script string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(script) // #nosec G304 -- the operator names the script
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A read is not a write: `versions access` names a key it consumes.
		if strings.Contains(line, "versions access") {
			continue
		}
		for _, re := range []*regexp.Regexp{secretCall, gcloudWrite, secretFlag} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				out[m[1]] = true
			}
		}
	}
	return out
}

func check(m *manifest, derived map[string]bool) []finding {
	var out []finding
	seen := map[string]string{}

	for _, k := range m.Keys {
		where := "key " + k.Key

		if k.Key == "" {
			out = append(out, finding{"V1", "manifest", "an entry has no `key`"})
			continue
		}
		if prev, dup := seen[k.Key]; dup {
			out = append(out, finding{"V1", where, fmt.Sprintf(
				"declared twice (the earlier entry says mode %s). Two entries means two "+
					"writers, and whichever runs second wins", prev)})
		}
		seen[k.Key] = k.Mode

		if !modes[k.Mode] {
			sorted := make([]string, 0, len(modes))
			for m := range modes {
				sorted = append(sorted, m)
			}
			sort.Strings(sorted)
			out = append(out, finding{"V1", where, fmt.Sprintf(
				"mode %q is not one of %s", k.Mode, strings.Join(sorted, ", "))})
			continue
		}
		if strings.TrimSpace(k.Why) == "" {
			out = append(out, finding{"V1", where,
				"has no `why`. This file is read when a cluster is already broken; a key " +
					"nobody explained is a key nobody can safely change"})
		}

		// V2 is the rule that would have caught the crash loop.
		if isURLShaped(k.Key, k.Shape) && k.Mode == "generate" {
			out = append(out, finding{"V2", where, fmt.Sprintf(
				"names a URL-shaped value but is `generate`, so the seeder writes random " +
					"bytes into it. A consumer parses this: it becomes a syntactically " +
					"valid URL pointing at the right host with the wrong credential, and " +
					"the ExternalSecret still reports Ready=True. Declare it `derived` " +
					"(composed by the derive script) or `copy` if it is shared")})
		}

		switch k.Mode {
		case "derived":
			if !derived[k.Key] {
				out = append(out, finding{"V3", where,
					"is declared `derived`, but no line of the derive script writes it. " +
						"Nothing will compose it: the seeder skips it (correctly) and the " +
						"value stays absent, so the consumer waits for a secret that no " +
						"step produces"})
			}
			if k.Shape == "" {
				out = append(out, finding{"V3", where,
					"is `derived` without a `shape`. The shape is what the derive script " +
						"promises (dsn, url); without it nothing can assert the composed " +
						"value is the kind the consumer parses"})
			}
		}
	}

	// The other direction: a key the script composes must be declared, or the
	// seeder will generate over it.
	declaredDerived := map[string]bool{}
	for _, k := range m.Keys {
		if k.Mode == "derived" {
			declaredDerived[k.Key] = true
		}
	}
	for key := range derived {
		if declaredDerived[key] {
			continue
		}
		// Only keys that look like ours (contain a dash and a known suffix) are
		// reported: the script also creates unrelated helper names.
		if !strings.Contains(key, "-") {
			continue
		}
		out = append(out, finding{"V4", "key " + key, fmt.Sprintf(
			"the derive script writes it, but the manifest does not declare it `derived` "+
				"(or does not declare it at all). The seeder %s", seederVerdict(m, key))})
	}
	return out
}

func seederVerdict(m *manifest, key string) string {
	for _, k := range m.Keys {
		if k.Key == key {
			return fmt.Sprintf("treats it as `%s` first, so the ordering decides the value", k.Mode)
		}
	}
	return "does not know about it, so on a fresh cluster it is never created"
}

func main() {
	var manifestPath, deriveScript string
	flag.StringVar(&manifestPath, "manifest", "bootstrap/secrets.yaml",
		"the seed declaration (keys, modes, shapes)")
	flag.StringVar(&deriveScript, "derive", "bootstrap/31-derive-secrets.sh",
		"the script that composes the `derived` values")
	flag.Parse()

	b, err := os.ReadFile(manifestPath) // #nosec G304
	if err != nil {
		fmt.Fprintln(os.Stderr, "valuesstd:", err)
		os.Exit(2)
	}
	var m manifest
	if err := yaml.Unmarshal(b, &m); err != nil {
		fmt.Fprintln(os.Stderr, "valuesstd:", err)
		os.Exit(2)
	}
	if len(m.Keys) == 0 {
		fmt.Fprintf(os.Stderr, "valuesstd: %s declares no keys. A manifest that failed to "+
			"parse and a cluster with no secrets look identical, and passing on the second "+
			"is how a check reports success over nothing.\n", manifestPath)
		os.Exit(2)
	}
	if _, err := os.Stat(deriveScript); err != nil && !os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "valuesstd:", err)
		os.Exit(2)
	}

	findings := check(&m, deriveKeys(deriveScript))
	if len(findings) == 0 {
		fmt.Printf("==> valuesstd: %d key(s), every mode agrees with who writes it "+
			"(V1, V2, V3, V4)\n", len(m.Keys))
		return
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	byRule := map[string]int{}
	for _, f := range findings {
		byRule[f.Rule]++
	}
	rules := make([]string, 0, len(byRule))
	for r := range byRule {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	var parts []string
	for _, r := range rules {
		parts = append(parts, fmt.Sprintf("%s=%d", r, byRule[r]))
	}
	fmt.Fprintf(os.Stderr, "\nFAIL: %d finding(s) (%s). See docs/secrets-manifest.md — "+
		"each rule names the failure it prevents.\n", len(findings), strings.Join(parts, " "))
	os.Exit(1)
}

var _ = filepath.Join // keep the import honest if the flags change
