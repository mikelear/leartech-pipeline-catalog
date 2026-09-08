// authconformance is the estate's auth standard, as a gate.
//
// It runs from a consumer repo's root in the PR and Release pipelines and fails
// the build when a deployed service's CODE or CHART departs from the standard.
// Every rule below exists because the estate paid for it once already; the
// failure text says which, because a gate whose message is "policy violation"
// teaches nobody and gets suppressed.
//
// THE STANDARD, for a service deployed into these clusters:
//
//	go-common >= v1.2.0        earlier versions have a conditional audience
//	                           check and a silent fail-open noopClient.
//	auth.Verifier for inbound  a service that only VALIDATES tokens is a
//	                           resource server. The dual-role ServiceClient
//	                           demands ClientID+Secret it never spends, which
//	                           is what took plan-api off the air on 2026-08-13.
//	two env vars, both public  LEARTECH_AUTH_ISSUER + LEARTECH_AUTH_AUDIENCE.
//	no disable flag            nothing that can turn auth off or make it
//	                           conditional, in code or chart.
//
// SCOPE. Only repos that are BOTH a deployed service (a chart with a
// Chart.yaml) AND an auth consumer (they import go-common's auth package).
// Libraries, CLIs and chart-only repos are skipped, loudly — see the
// applicability report, which prints why rather than exiting silently.
//
// Stdlib only, deliberately: the mk runs this via `go run` in a throwaway
// module, so a dependency would mean a network fetch and a go.sum to keep
// current in every consumer repo.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// minGoCommon is the floor. Below this, Config.Audience is checked only when
// non-empty and an empty ServerURL yields a pass-through middleware.
var minGoCommon = version{1, 2, 0}

// disableFlags must not appear in code or chart. A flag that can make auth
// optional will eventually be set, on the cluster where it matters, by someone
// debugging something else.
var disableFlags = []string{
	"AUTH_ENABLED",
	"AUTH_REQUIRED",
	"AUTH_DISABLED",
	"SKIP_AUTH",
	"LEARTECH_AUTH_REQUIRED",
}

// requiredChartEnv is the whole inbound contract.
var requiredChartEnv = []string{
	"LEARTECH_AUTH_ISSUER",
	"LEARTECH_AUTH_AUDIENCE",
}

// forbiddenChartEnv: names from the envconfig-derived era, plus credentials a
// resource server does not spend, plus the one that reads like the issuer knob
// and is not.
var forbiddenChartEnv = []string{
	"AUTH_SERVERURL", "AUTH_CLIENTID", "AUTH_CLIENTSECRET", "AUTH_AUDIENCE", "AUTH_TOKENURL",
	"LEARTECH_AUTH_SERVER_URL",
}

// outboundCalls prove a service genuinely mints its own tokens, which is the
// only justification for the dual-role client.
var outboundCalls = []string{"GetAuthToken", "SetAuthHeader", "AuthenticatedClient", "HTTPClient"}

// exemption is a repo's declared, justified departure from the standard, read
// from .authconformance in the repo root:
//
//	issuer-env: HYDRA_PUBLIC_URL
//	reason: this service IS the issuer — the same URL is its Hydra public
//	  endpoint and is already required config, so a second variable carrying
//	  the identical value would be a name to keep in sync, not a control.
//
// A reason is MANDATORY. The point is not to allow exceptions cheaply — it is
// that an exception should arrive in a diff, with an argument attached, where a
// reviewer can disagree with it. A gate with no exemption mechanism gets
// deleted the first time it is wrong; one with a silent bypass stops meaning
// anything.
type exemption struct {
	issuerEnv string
	reason    string
}

func loadExemption(root string, r *report) exemption {
	var e exemption
	var last string
	b, err := os.ReadFile(filepath.Join(root, ".authconformance"))
	if err != nil {
		return e
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// A reason is usually a paragraph, so anything that is not one of the
		// known keys continues the previous one. Without this the middle of a
		// wrapped reason is silently dropped and the printed justification
		// reads as nonsense — which is worse than no justification, because it
		// looks like one.
		k, v, hasColon := strings.Cut(line, ":")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case hasColon && k == "issuer-env":
			e.issuerEnv = v
			last = "issuer-env"
		case hasColon && k == "reason":
			e.reason = v
			last = "reason"
		case last == "reason":
			e.reason += " " + line
		}
	}
	if e.issuerEnv != "" && e.reason == "" {
		r.fail("exemption", ".authconformance",
			"declares issuer-env: %s with no `reason:`. An undocumented exemption is a silent bypass — state why this service cannot use LEARTECH_AUTH_ISSUER.", e.issuerEnv)
	}
	if e.issuerEnv != "" {
		fmt.Printf("    exemption: issuer supplied by %s — %s\n", e.issuerEnv, e.reason)
	}
	return e
}

type finding struct {
	rule string
	file string
	msg  string
}

type report struct {
	findings []finding
	// counters, so "no findings" can be distinguished from "looked at nothing"
	goFiles, chartFiles int
}

func (r *report) fail(rule, file, format string, a ...any) {
	r.findings = append(r.findings, finding{rule, file, fmt.Sprintf(format, a...)})
}

// standard is printed on demand and on failure. The gate is fetched and run
// locally (the mk curls it), so for a developer or a coding agent this is often
// the FIRST statement of what auth is expected to look like — before CI pushes
// back. A gate that only says "no" makes people guess; this says what "yes"
// is, and why each rule exists.
const standard = `
THE LEARTECH AUTH STANDARD — for any service deployed into these clusters.

  1. go-common >= v1.2.0
     Earlier versions check RFC 8707 audience only when Audience is non-empty,
     so a service that leaves it unset enforces issuer and permissions but NOT
     audience — any token this issuer signed, for any service, is accepted.
     They also hand back a pass-through noopClient when ServerURL is empty.

  2. auth.NewVerifier for inbound; auth.NewServiceClient ONLY with a real
     outbound leg.
     A service that only VALIDATES tokens is an OAuth2 resource server. The
     dual-role ServiceClient's validateConfig demands ClientID and ClientSecret
     it never spends; their absence took plan-api off the air on 2026-08-13.
     Forwarding an inbound bearer to a peer is NOT an outbound leg.

  3. Exactly two inbound env vars, both public, both required:
       LEARTECH_AUTH_ISSUER     RFC 7519 4.1.1 — the identity a token must
                                claim. Per-cluster; never a shared default.
       LEARTECH_AUTH_AUDIENCE   RFC 8707 — this service's own audience.
     No client credentials in the chart: a resource server holds no identity.

  4. Nothing that can disable or relax auth, in code or chart.
     Not AUTH_ENABLED, AUTH_REQUIRED, SKIP_AUTH, AUTH_DISABLED or
     LEARTECH_AUTH_REQUIRED; not auth.enabled / auth.required in values.
     v1.2.0+ has no runtime disable path, and a flag that can relax auth will
     eventually be set, on the cluster where it matters, by someone debugging
     something else. NAMING one in an inert/ignored-vars list is fine and
     encouraged — that reports it as ignored. READING one is not.

  5. A genuine exception is DECLARED, in .authconformance, with a reason:
       issuer-env: HYDRA_PUBLIC_URL
       reason: this service IS the issuer — ...
     The reason is mandatory. An exception should arrive in a diff with an
     argument attached, where a reviewer can disagree with it.

Prove it in tests, not prose: pair every accept with a refuse that differs by
one variable. A lone refusal proves nothing — everything refuses when the rig
is misconfigured.
`

func main() {
	root := "."
	for _, a := range os.Args[1:] {
		if a == "--explain" || a == "-explain" || a == "explain" {
			fmt.Print(standard)
			return
		}
		root = a
	}

	svc, why := applicable(root)
	fmt.Printf("==> auth-conformance: %s\n", why)
	if !svc {
		return
	}

	var r report
	ex := loadExemption(root, &r)
	checkGoCommonFloor(root, &r)
	checkGoSource(root, &r)
	checkChart(root, ex, &r)

	// A gate that examined nothing looks exactly like a gate that found
	// nothing. Make the difference loud.
	if r.goFiles == 0 {
		r.fail("self-check", "", "examined 0 Go files — the walk is broken, not the repo")
	}
	if r.chartFiles == 0 {
		r.fail("self-check", "", "examined 0 chart files — the walk is broken, not the repo")
	}
	fmt.Printf("    examined %d Go files, %d chart files\n", r.goFiles, r.chartFiles)

	if len(r.findings) == 0 {
		fmt.Println("==> auth-conformance: PASS  (run with --explain to print the standard)")
		return
	}

	sort.Slice(r.findings, func(i, j int) bool { return r.findings[i].rule < r.findings[j].rule })
	fmt.Fprintf(os.Stderr, "\n==> auth-conformance: FAIL (%d)\n\n", len(r.findings))
	for _, f := range r.findings {
		loc := f.file
		if loc == "" {
			loc = "(repo)"
		}
		fmt.Fprintf(os.Stderr, "  [%s] %s\n      %s\n\n", f.rule, loc, f.msg)
	}
	fmt.Fprint(os.Stderr, standard)
	os.Exit(1)
}

// applicable reports whether this repo is a deployed service that consumes
// go-common auth, and always explains the verdict.
func applicable(root string) (bool, string) {
	charts := chartDirs(root)
	if len(charts) == 0 {
		return false, "no chart found — not a deployed service, skipping"
	}
	if !importsGoCommonAuth(root) {
		return false, fmt.Sprintf("chart present (%s) but no import of leartech-go-common/pkg/auth — not an auth consumer, skipping", strings.Join(charts, ", "))
	}
	return true, fmt.Sprintf("deployed service with go-common auth (%s) — enforcing", strings.Join(charts, ", "))
}

func chartDirs(root string) []string {
	var out []string
	base := filepath.Join(root, "charts")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, e.Name(), "Chart.yaml")); err == nil {
			out = append(out, filepath.Join("charts", e.Name()))
		}
	}
	return out
}

func importsGoCommonAuth(root string) bool {
	found := false
	walkGo(root, func(path string, f *ast.File) {
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "leartech-go-common/pkg/auth") {
				found = true
			}
		}
	})
	return found
}

// ── go-common floor ────────────────────────────────────────────────────────

type version struct{ major, minor, patch int }

func (v version) String() string { return fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch) }
func (v version) lessThan(o version) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

var goCommonRE = regexp.MustCompile(`leartech-go-common\s+v(\d+)\.(\d+)\.(\d+)`)

func checkGoCommonFloor(root string, r *report) {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		r.fail("go-common-floor", "go.mod", "cannot read go.mod: %v", err)
		return
	}
	m := goCommonRE.FindStringSubmatch(string(b))
	if m == nil {
		r.fail("go-common-floor", "go.mod", "this repo imports leartech-go-common/pkg/auth but go.mod declares no leartech-go-common version — the parser or the require line has changed")
		return
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	got := version{maj, min, pat}
	if got.lessThan(minGoCommon) {
		r.fail("go-common-floor", "go.mod",
			"leartech-go-common %s is below the floor %s. Before %s the RFC 8707 audience check is CONDITIONAL (`if cfg.Audience != \"\"`), so a service that leaves Audience unset enforces issuer and permissions but not audience — any token this issuer signed, minted for any service, is accepted. Earlier versions also return a pass-through noopClient when ServerURL is empty.",
			got, minGoCommon, minGoCommon)
	}
}

// ── code ───────────────────────────────────────────────────────────────────

func checkGoSource(root string, r *report) {
	var (
		serviceClientAt []string
		hasOutbound     bool
		fset            = token.NewFileSet()
	)

	walkGoFset(root, fset, func(path string, f *ast.File) {
		r.goFiles++
		isTest := strings.HasSuffix(path, "_test.go")

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := sel.Sel.Name

			if name == "NewServiceClient" && !isTest {
				serviceClientAt = append(serviceClientAt, fmt.Sprintf("%s:%d", path, fset.Position(call.Pos()).Line))
			}
			for _, oc := range outboundCalls {
				if name == oc && !isTest {
					hasOutbound = true
				}
			}
			return true
		})

		// Disable flags that actually FEED configuration — a struct tag that
		// binds one, or a direct os.Getenv on the literal.
		//
		// Deliberately NOT a plain string search. Several repos keep an
		// `inertAuthEnv` slice naming these very variables in order to WARN
		// that they are ignored, which is the opposite of a violation. A
		// substring match flags the fix as the defect, and a gate that
		// punishes the remedy gets switched off.
		if !isTest {
			for _, use := range disableFlagUses(f) {
				r.fail("no-disable-flag", path,
					"binds %q via %s. Auth is not conditional: go-common v1.2.0+ has no runtime disable path, and a flag that can relax it will eventually be set on the cluster where it matters. Naming it in an inert/ignored-vars list is fine; reading it is not.",
					use.name, use.how)
			}
		}
	})

	if len(serviceClientAt) > 0 && !hasOutbound {
		r.fail("verifier-for-inbound", serviceClientAt[0],
			"constructs auth.NewServiceClient (%s) but never spends an outbound token (no %s). A service that only VALIDATES tokens is an OAuth2 resource server and must use auth.NewVerifier, which asks for Issuer+Audience and nothing else. ServiceClient's validateConfig additionally demands ClientID and ClientSecret — credentials this service never uses — and their absence took plan-api off the air on 2026-08-13. NOTE: forwarding an inbound bearer to a peer is NOT an outbound leg.",
			strings.Join(serviceClientAt, ", "), strings.Join(outboundCalls, "/"))
	}
}

type flagUse struct{ name, how string }

// disableFlagUses finds the two ways a disable flag can actually reach the
// running config: an envconfig/env struct tag, or os.Getenv on a literal.
func disableFlagUses(f *ast.File) []flagUse {
	var out []flagUse
	seen := map[string]bool{}
	add := func(name, how string) {
		if !seen[name+how] {
			seen[name+how] = true
			out = append(out, flagUse{name, how})
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Field:
			if v.Tag == nil {
				return true
			}
			tag := v.Tag.Value
			if !strings.Contains(tag, "env:") && !strings.Contains(tag, "envconfig:") {
				return true
			}
			for _, flag := range disableFlags {
				if strings.Contains(tag, `"`+flag+`"`) || strings.Contains(tag, `:"`+flag+`,`) ||
					strings.Contains(tag, `"`+flag+`,`) {
					add(flag, "a struct tag")
				}
			}
		case *ast.CallExpr:
			sel, ok := v.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Getenv" || len(v.Args) == 0 {
				return true
			}
			bl, ok := v.Args[0].(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			lit := strings.Trim(bl.Value, "`\"")
			for _, flag := range disableFlags {
				if lit == flag {
					add(flag, "os.Getenv")
				}
			}
		}
		return true
	})
	return out
}

func walkGo(root string, fn func(string, *ast.File)) {
	walkGoFset(root, token.NewFileSet(), fn)
}

func walkGoFset(root string, fset *token.FileSet, fn func(string, *ast.File)) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", ".git", "node_modules", "docs":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		fn(path, f)
		return nil
	})
}

// ── chart ──────────────────────────────────────────────────────────────────

var envNameRE = regexp.MustCompile(`(?m)^\s*-\s*name:\s*([A-Z][A-Z0-9_]*)\s*$`)

func checkChart(root string, ex exemption, r *report) {
	declared := map[string]string{} // env name -> file it appeared in
	var valuesFiles []string

	for _, dir := range chartDirs(root) {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			r.chartFiles++
			src := string(b)
			for _, m := range envNameRE.FindAllStringSubmatch(src, -1) {
				if _, seen := declared[m[1]]; !seen {
					declared[m[1]] = path
				}
			}
			if strings.HasSuffix(d.Name(), "values.yaml") {
				valuesFiles = append(valuesFiles, path)
			}
			return nil
		})
	}

	for _, want := range requiredChartEnv {
		if _, ok := declared[want]; ok {
			continue
		}
		// An exemption substitutes ONE name for the issuer. It still has to be
		// rendered — otherwise the exemption would excuse the variable being
		// absent altogether, which is the failure it was meant to describe.
		if want == "LEARTECH_AUTH_ISSUER" && ex.issuerEnv != "" {
			if _, rendered := declared[ex.issuerEnv]; rendered {
				continue
			}
			r.fail("chart-env-contract", "charts/",
				"declares an exemption naming %s as the issuer variable, but the chart does not render %s either. The exemption excuses a different NAME, not a missing value.",
				ex.issuerEnv, ex.issuerEnv)
			continue
		}
		r.fail("chart-env-contract", "charts/",
			"does not declare %s. The inbound contract is exactly %s — both public, both required, no secret. A service missing either cannot verify and must not serve. If this service genuinely supplies the issuer under another name, declare it in .authconformance with a reason.",
			want, strings.Join(requiredChartEnv, " + "))
	}
	for _, bad := range append(append([]string{}, forbiddenChartEnv...), disableFlags...) {
		if f, ok := declared[bad]; ok {
			r.fail("chart-env-contract", f,
				"declares %s. It is either a non-standard name from the envconfig-derived era, a client credential a resource server does not spend, or a flag that can relax auth.", bad)
		}
	}

	for _, vf := range valuesFiles {
		for _, key := range authValuesKeys(vf) {
			switch key {
			case "enabled", "required", "disabled", "skip", "optional":
				r.fail("no-disable-flag", vf, "declares auth.%s — auth must not be conditional.", key)
			case "clientId", "clientid", "clientSecret", "clientsecret":
				r.fail("verifier-for-inbound", vf,
					"declares auth.%s. A resource server spends no outbound token and holds no client identity; these were only ever load-bearing as input to the dual-role ServiceClient's validateConfig.", key)
			}
		}
	}
}

// authValuesKeys returns keys nested directly under a top-level `auth:` block.
func authValuesKeys(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var keys []string
	var in bool
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "auth:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			break
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		// Only direct children: exactly two spaces of indent.
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") {
			continue
		}
		if k, _, ok := strings.Cut(t, ":"); ok {
			keys = append(keys, strings.TrimSpace(k))
		}
	}
	return keys
}
