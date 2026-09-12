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

// forbiddenChartEnv is always wrong: names from the envconfig-derived era that
// match nothing any service reads, plus the one that reads like the issuer knob
// and is not.
var forbiddenChartEnv = []string{
	"AUTH_SERVERURL", "AUTH_CLIENTID", "AUTH_CLIENTSECRET", "AUTH_AUDIENCE", "AUTH_TOKENURL",
}

// outboundOnlyChartEnv is wrong ONLY for a service with no outbound leg.
//
// These were in forbiddenChartEnv unconditionally, which was wrong: a service
// that genuinely MINTS tokens — leartech-mcp-servers builds per-server s2s
// clients — legitimately needs a client identity, and flagging it would have
// told a correct service to delete credentials it actually spends. The code
// rule already distinguishes the two roles by looking for outbound calls; the
// chart rule has to use the same signal or it contradicts it.
// LEARTECH_AUTH_SERVER_URL is here rather than in forbiddenChartEnv for the
// same reason. It is go-common Config.ServerURL — the TOKEN ENDPOINT a
// ServiceClient posts to, not the issuer a Verifier validates against. For a
// resource server it is a decoy that reads like the issuer knob and does
// nothing; for a service that mints tokens it is required.
var outboundOnlyChartEnv = []string{
	"LEARTECH_AUTH_CLIENT_ID", "LEARTECH_AUTH_CLIENT_SECRET", "LEARTECH_AUTH_TARGET_AUDIENCE",
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

       This includes optional: true on a credential's secretKeyRef, which is
       the same trade in chart form: the pod STARTS with an empty client id and
       secret, and the first token mint fails downstream as a 401 attributed to
       the wrong layer. Measured 2026-09-12: 19 such mounts across 14 services,
       concealing one identity that existed in no secret backend at all and had
       run on an empty secret for months. Without the flag a missing secret is
       CreateContainerConfigError at pod start — unmissable, attributable, fixed
       in minutes. Still allowed on genuinely optional things (Redis passwords,
       GitHub tokens).

  5. A genuine exception is DECLARED, in .authconformance, with a reason:
       issuer-env: HYDRA_PUBLIC_URL
       reason: this service IS the issuer — ...
     The reason is mandatory. An exception should arrive in a diff with an
     argument attached, where a reviewer can disagree with it.

    6. The service DECLARES what kind of auth participant it is, in
       .authprofile, and the tool cross-checks that against the code:
         type: inbound-resource-server   validates tokens, mints none
         type: dual-role                 validates AND mints
         type: outbound-only             mints only; has no audience of its own
         type: public-resource-server    inbound, for external DCR clients;
                                         audience is its own URL (RFC 8707
                                         resource indicator) published via
                                         RFC 9728 protected-resource metadata
         type: issuer                    the auth service; needs a reason,
                                         because it validates what it mints and
                                         so nothing can contradict it
         type: none                      does not participate in auth
       There is NO single standard env set — the correct one is a function of
       the type. An inbound-only service holding CLIENT_ID/CLIENT_SECRET looks
       like it carries an identity it never spends, which is how inert
       credentials reached several charts and how an operator comes to believe
       a variable is load-bearing when nothing reads it.
       Declared AND derived, because neither works alone: derivation guesses
       (a UUID-based heuristic once reported the platform's own SPA client as a
       stranger holding internal audiences), and declaration drifts (someone
       adds an outbound call and the declaration becomes a comment that lies).
       It fails on CONTRADICTIONS the evidence proves, never on absence of
       evidence — a constructor behind a helper is reported, not failed.

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
	var ev evidence
	ex := loadExemption(root, &r)
	checkGoCommonFloor(root, &r)
	hasOutboundLeg := checkGoSource(root, &r, &ev)
	checkChart(root, ex, hasOutboundLeg, &r)

	// Profile last: it reports what the earlier walks proved, so it must run
	// after them.
	prof := loadProfile(root, &r, ev)
	checkProfile(prof, ev, &r)

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

// fileImportsAuth reports whether this file imports go-common/pkg/auth. Used to
// qualify generically-named calls (Middleware, BearerAuth) so they only count
// as inbound auth evidence where the auth package is actually in scope.
func fileImportsAuth(f *ast.File) bool {
	for _, im := range f.Imports {
		if im.Path == nil {
			continue
		}
		if strings.Contains(im.Path.Value, "leartech-go-common/pkg/auth") {
			return true
		}
	}
	return false
}

func checkGoSource(root string, r *report, ev *evidence) (hasOutboundLeg bool) {
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
				at := fmt.Sprintf("%s:%d", path, fset.Position(call.Pos()).Line)
				serviceClientAt = append(serviceClientAt, at)
				ev.serviceClientAt = append(ev.serviceClientAt, at)
			}
			// THE INBOUND HALF, WHICH HAS TWO SHAPES.
			//
			// NewVerifier is the obvious one. But a dual-role service validates
			// inbound through its ServiceClient — maestro does
			// `au.Middleware(nil)` and never calls NewVerifier at all — so
			// looking only for NewVerifier derived maestro as "outbound-only"
			// when it is dual-role. Middleware and BearerAuth are the other
			// shape.
			//
			// Gated on the FILE importing go-common/pkg/auth, because
			// "Middleware" is an extremely common method name and an unguarded
			// match would call any gin middleware an inbound auth gate. That
			// error points the wrong way: it would fail a genuinely
			// outbound-only service for validating tokens it does not.
			if !isTest && (name == "NewVerifier" || name == "Middleware" || name == "BearerAuth") {
				if fileImportsAuth(f) {
					ev.verifierAt = append(ev.verifierAt,
						fmt.Sprintf("%s:%d", path, fset.Position(call.Pos()).Line))
				}
			}
			for _, oc := range outboundCalls {
				if name == oc && !isTest {
					hasOutbound = true
					ev.outboundAt = append(ev.outboundAt,
						fmt.Sprintf("%s:%d", path, fset.Position(call.Pos()).Line))
				}
			}
			return true
		})

		if !isTest && ev != nil {
			if src, rerr := os.ReadFile(path); rerr == nil &&
				strings.Contains(string(src), "oauth-protected-resource") {
				ev.protectedResAt = append(ev.protectedResAt, path)
			}
		}

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
	return hasOutbound
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

// credentialEnv are the env vars whose secret MUST exist for the service to
// function. A missing one is a deploy-time fault, not a runtime one.
var credentialEnv = []string{
	"LEARTECH_AUTH_CLIENT_ID",
	"LEARTECH_AUTH_CLIENT_SECRET",
}

// checkOptionalCredentials fails on `optional: true` under a credential's
// secretKeyRef.
//
// WHY THIS IS A GATE AND NOT A LINT SUGGESTION. `optional: true` is the same
// trade as an AUTH_ENABLED flag, which this tool already refuses: it converts a
// PROVISIONING failure into a silent RUNTIME one. The pod starts with an empty
// client id and secret and the first token mint fails downstream as a 401
// attributed to the wrong layer.
//
// Measured across the estate on 2026-09-12: 19 live auth-credential mounts
// carried it across 14 services, the orchestrator controller among them. It had
// concealed an entire missing identity — leartech-lighthouse-pr-events mounts
// its client secret optional:true, that secret existed in neither GSM nor the
// cluster, and the pod had been running with an EMPTY secret for months instead
// of failing to start. Nothing reported it because an empty credential and a
// working one are indistinguishable until something tries to mint.
//
// Without the flag a missing secret is CreateContainerConfigError at pod start:
// unmissable, attributable, and fixed in minutes.
//
// Comment lines are skipped, so prose explaining the rule — including the
// comment a chart author writes when removing it — cannot trip it.
func checkOptionalCredentials(src, path string, r *report) {
	lines := strings.Split(src, "\n")

	for i, line := range lines {
		m := envNameRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if !containsStr(credentialEnv, name) {
			continue
		}

		// Scan this env entry only: it ends at the next `- name:` or a
		// dedent to the list level.
		for j := i + 1; j < len(lines) && j < i+12; j++ {
			next := lines[j]
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if envNameRE.MatchString(next) || strings.HasPrefix(trimmed, "- ") {
				break
			}
			if strings.Contains(trimmed, "optional: true") {
				r.fail("no-optional-credential", path,
					"mounts %s with `optional: true`. The pod will START with an empty credential and fail later at token mint, as a 401 attributed to the wrong layer. This is the same trade as an AUTH_ENABLED flag. Drop it so a missing secret is CreateContainerConfigError at pod start.",
					name)
				break
			}
		}
	}
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func checkChart(root string, ex exemption, hasOutboundLeg bool, r *report) {
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
			checkOptionalCredentials(src, path, r)
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
		// A dual-role service configures auth.Config, which has NO Issuer field:
		// ServerURL is both the token endpoint it posts to and the base the JWKS
		// is derived from. So for a service that mints tokens,
		// LEARTECH_AUTH_SERVER_URL genuinely IS the issuer, and demanding a
		// separate LEARTECH_AUTH_ISSUER would be asking for a variable go-common
		// does not read in that role.
		//
		// This is the counterpart to outboundOnlyChartEnv listing SERVER_URL:
		// for a pure resource server it is a decoy that reads like the issuer
		// knob and is not one; for a minting service it is the issuer.
		if want == "LEARTECH_AUTH_ISSUER" && hasOutboundLeg {
			if _, rendered := declared["LEARTECH_AUTH_SERVER_URL"]; rendered {
				continue
			}
		}
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
				"declares %s. It is either a non-standard name from the envconfig-derived era or a flag that can relax auth — neither is read by any service.", bad)
		}
	}

	// Client credentials are a defect only for a service that spends no token.
	if !hasOutboundLeg {
		for _, bad := range outboundOnlyChartEnv {
			if f, ok := declared[bad]; ok {
				r.fail("verifier-for-inbound", f,
					"declares %s, but no Go source in this repo spends an outbound token (no %s). A pure resource server holds no client identity, and an unused credential that gates startup is what took plan-api off the air on 2026-08-13.",
					bad, strings.Join(outboundCalls, "/"))
			}
		}
	}

	for _, vf := range valuesFiles {
		for _, key := range authValuesKeys(vf) {
			switch key {
			case "enabled", "required", "disabled", "skip", "optional":
				r.fail("no-disable-flag", vf, "declares auth.%s — auth must not be conditional.", key)
			case "clientId", "clientid", "clientSecret", "clientsecret":
				if hasOutboundLeg {
					continue
				}
				r.fail("verifier-for-inbound", vf,
					"declares auth.%s, but no Go source in this repo spends an outbound token. A pure resource server holds no client identity; these were only ever load-bearing as input to the dual-role ServiceClient's validateConfig.", key)
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
