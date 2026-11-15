package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// runContract is the presubmit: everything about a pack and its declaration that
// can be decided without a cluster, decided here.
//
// The list below is not hypothetical. Each check exists because the estate paid
// for it once, and the finding text names which:
//
//	healthPath unset            next-generation-lending-website#9 — declared /health,
//	                            probed /health/live
//	probe URL host undefined    leartech-auth-ui#111 — gate probe 404'd on a hydra
//	                            host the preview chart never creates
//	seed that is a check        end2end-ui ran 21 auth-service scripts as "seeds"
//	                            with failures swallowed
//	script with a hardcoded host leartech-mortgagesourcing-web#18 — smoke hit a URL
//	                            baked into the script instead of PREVIEW_URL
//	unknown env key             a repo believed it had declared a health path the
//	                            task never read
//	NONE beside a pack          the "undeclared" branch, made static
//
// allFindings is the whole check, as a list. The CLI prints it; the tests assert
// on it. One implementation, so a rule that passes a test also reaches the gate.
func allFindings(repoDir, lane string) []string {
	findings := []string{}
	add := func(format string, a ...interface{}) {
		findings = append(findings, fmt.Sprintf(format, a...))
	}

	// A repo that declares it has no suite has nothing to declare a preview FOR:
	// end2end/NONE is the whole statement, and asking it for a healthPath would
	// make the opt-out cost more than the suite.
	nonePath := filepath.Join(repoDir, "end2end", "NONE")
	_, noneErr := os.Stat(nonePath)
	if noneErr == nil {
		if _, err := os.Stat(filepath.Join(repoDir, ContractFile)); err == nil {
			add("end2end/NONE and %s are both present. A repo either runs a suite or "+
				"declares that it does not; keeping both means nobody can tell which is "+
				"authoritative.", ContractFile)
		}
		return findings
	}

	c, contractFindings := LoadContract(repoDir)
	findings = append(findings, contractFindings...)

	scripts := packScripts(repoDir)
	if len(scripts) == 0 {
		add("no pack scripts under end2end/ and no end2end/NONE. Add 0X-*.sh " +
			"checks, or add end2end/NONE with one line saying why this service has " +
			"no end-to-end suite.")
	}

	for _, s := range scripts {
		info, err := os.Stat(s)
		if err != nil {
			add("%s: cannot stat: %v", rel(repoDir, s), err)
			continue
		}
		if info.Mode()&0o111 == 0 {
			add("%s is not executable. The runner invokes it with bash, so this does not "+
				"fail today — it fails when someone moves it to the other lane, which runs "+
				"only executable files.", rel(repoDir, s))
		}
		if host := hardcodedHost(s); host != "" {
			add("%s pins the host %q. A check that hardcodes a host tests that host "+
				"forever, and passes on a preview that never deployed: take the base "+
				"from ${PREVIEW_URL} (or ${STAGING_URL} for the observer).", rel(repoDir, s), host)
		}
	}

	if c != nil {
		for _, seed := range c.Seeds {
			p := filepath.Join(repoDir, seed)
			info, err := os.Stat(p)
			if err != nil {
				add("%s: seed %q does not exist.", ContractFile, seed)
				continue
			}
			if info.Mode()&0o111 == 0 {
				add("%s: seed %q is not executable.", ContractFile, seed)
			}
			if SeedNameLooksLikeACheck(seed) {
				add("%s: seed %q looks like a CHECK, not setup. The UI lane runs its seeds "+
					"before, and again before Playwright, with failures swallowed — on "+
					"leartech-auth-service that meant the functional suite ran twice and "+
					"reported its failures as 'continuing'. Name a seed for what it sets up "+
					"(register/seed/setup/reset/fixture/bootstrap) or move it back into the pack.",
					ContractFile, seed)
			}
		}
		for _, p := range c.Probes {
			for _, host := range probeHosts(p.URL) {
				if hostAppearsInCharts(repoDir, host) {
					continue
				}
				add("%s: probe %q names the host %q, which no chart under preview/ "+
					"creates. That is the shape of leartech-auth-ui#111, where the gate "+
					"held a ready preview closed for 10 minutes waiting on a 404'd hydra URL. "+
					"If the host is created at runtime, template it (${NAMESPACE}) so this "+
					"check can see that.", ContractFile, p.URL, host)
			}
		}
	}

	findings = append(findings, unknownEnvKeys(repoDir)...)

	if lane == "ui" {
		findings = append(findings, uiSpecFindings(repoDir)...)
	}
	return findings
}

func runContract(args []string) int {
	fs := flag.NewFlagSet("e2e contract", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository root")
	lane := fs.String("check-name", "web", "which lane: web or ui")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	findings := allFindings(*dir, *lane)
	sort.Strings(findings)
	if len(findings) == 0 {
		fmt.Printf("==> e2e contract: ok (%d pack script(s))\n", len(packScripts(*dir)))
		return 0
	}
	fmt.Printf("==> e2e contract: %d finding(s)\n\n", len(findings))
	for _, f := range findings {
		fmt.Printf("  - %s\n\n", f)
	}
	fmt.Println("Each of these is checkable without a cluster, which is the point: a PR")
	fmt.Println("that gets its declaration wrong learns here in seconds, not from an")
	fmt.Println("end2end run that waited ten minutes and then said 'never returned 200'.")
	return 1
}

func runExplain(args []string) int {
	fs := flag.NewFlagSet("e2e explain", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	c, findings := LoadContract(*dir)
	if c == nil {
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, f)
		}
		return 1
	}
	fmt.Print(c.String())
	if len(findings) > 0 {
		fmt.Printf("\n%d finding(s):\n", len(findings))
		for _, f := range findings {
			fmt.Printf("  - %s\n", f)
		}
		return 1
	}
	return 0
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}

// packScripts returns the 0X-*.sh files the runner would execute, in order.
func packScripts(repoDir string) []string {
	matches, _ := filepath.Glob(filepath.Join(repoDir, "end2end", "[0-9][0-9]-*.sh"))
	sort.Strings(matches)
	return matches
}

var (
	curlURLCall = regexp.MustCompile(`https?://([A-Za-z0-9_.-]+\.[A-Za-z0-9_.-]+)`)
	// A URL that is built from a variable is the thing we want; only a literal
	// host in the script's own text is a finding.
	envExpansion = regexp.MustCompile(`\$\{?[A-Za-z_]`)
)

// hardcodedHost returns the first literal host in a script that is not part of a
// variable expansion, or "".
func hardcodedHost(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- path came from the pack glob
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // a comment may name a host; it is not a request
		}
		if strings.Contains(line, "BASE_URL") || strings.Contains(line, "PREVIEW_URL") ||
			strings.Contains(line, "STAGING_URL") {
			continue // the one place a host belongs is inside a variable
		}
		m := curlURLCall.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// If the URL came from a variable the literal host is not here anyway; a
		// match on a line that also expands an env var is a base URL being
		// composed, not pinned.
		if envExpansion.MatchString(line) && !strings.Contains(line, "://") {
			continue
		}
		if isBenignHost(m[1]) {
			continue
		}
		return m[1]
	}
	return ""
}

// isBenignHost filters hosts that are not "a preview": schema URLs in comments
// are already skipped, but a tool may legitimately call a public API.
func isBenignHost(host string) bool {
	for _, h := range []string{"localhost", "127.0.0.1", "example.com", "schema.org", "w3.org"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// probeHosts extracts concrete hosts from a probe URL, skipping templated ones.
func probeHosts(url string) []string {
	var out []string
	for _, m := range curlURLCall.FindAllStringSubmatch(url, -1) {
		host := m[1]
		if strings.Contains(host, "${") || strings.Contains(host, "$") {
			continue
		}
		out = append(out, host)
	}
	return out
}

func hostIsDeclarable(host string) bool {
	return host != ""
}

// hostAppearsInCharts looks for the host anywhere under preview/, which is where a
// repo's preview helmfile and chart values live.
func hostAppearsInCharts(repoDir, host string) bool {
	root := filepath.Join(repoDir, "preview")
	found := false
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || found {
			return nil
		}
		switch filepath.Ext(path) {
		case ".yaml", ".yml", ".gotmpl", ".tpl", ".json":
		default:
			return nil
		}
		b, readErr := os.ReadFile(path) // #nosec G304 -- walked under preview/
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(b), host) {
			found = true
		}
		return nil
	})
	return found
}

// unknownEnvKeys reads the consumer's .lighthouse end2end pipeline(s) and reports
// any END2END_*/E2E_* variable the catalog task does not read.
func unknownEnvKeys(repoDir string) []string {
	var findings []string
	paths, _ := filepath.Glob(filepath.Join(repoDir, ".lighthouse", "jenkins-x", "end2end*.yaml"))
	for _, p := range paths {
		b, err := os.ReadFile(p) // #nosec G304 -- repo under test
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "- name:") {
				continue
			}
			key := strings.TrimSpace(strings.TrimPrefix(trimmed, "- name:"))
			if !(strings.HasPrefix(key, "END2END_") || strings.HasPrefix(key, "E2E_")) {
				continue
			}
			if !knownEnvKeys[key] {
				findings = append(findings, fmt.Sprintf(
					"%s sets %s, which neither end2end task reads — the run silently uses "+
						"its default instead. Known keys: %s.",
					rel(repoDir, p), key, strings.Join(sortedKeys(knownEnvKeys), ", ")))
			}
		}
	}
	return findings
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uiSpecFindings checks the Playwright side: a spec that pins a host would pass
// against a preview it had no contact with.
func uiSpecFindings(repoDir string) []string {
	var findings []string
	specs, _ := filepath.Glob(filepath.Join(repoDir, "end2end-ui", "*.spec.ts"))
	root := filepath.Join(repoDir, "end2end-ui")
	more, _ := filepath.Glob(filepath.Join(root, "**", "*.spec.ts"))
	specs = append(specs, more...)
	if len(specs) == 0 {
		findings = append(findings, "end2end-ui lane declared but no *.spec.ts under "+
			"end2end-ui/. Playwright would report 'no tests found' and the lane would be "+
			"green having tested nothing.")
		return findings
	}
	for _, s := range specs {
		b, err := os.ReadFile(s) // #nosec G304 -- repo under test
		if err != nil {
			continue
		}
		text := string(b)
		if !strings.Contains(text, "PREVIEW_URL") && !strings.Contains(text, "baseURL") {
			findings = append(findings, fmt.Sprintf(
				"%s does not reference PREVIEW_URL or baseURL; if it navigates to a literal "+
					"host it tests that host.", rel(repoDir, s)))
		}
	}
	return findings
}
