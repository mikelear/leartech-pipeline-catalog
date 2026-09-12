package main

// The gate, gated.
//
// This checker fails other people's builds, so "it seemed to work when I ran it"
// is not good enough. Each rule gets a fixture repo that violates exactly one
// thing and a control that violates nothing, because a rule that fires on
// everything is indistinguishable from a rule that works.
//
// The controls matter more than the violations here. Two of these rules were
// WRONG on first contact with the estate: the disable-flag rule flagged the
// `inertAuthEnv` lists that exist to WARN that those variables are ignored —
// punishing the remedy — and the issuer rule flagged auth-service, which
// legitimately supplies the issuer as HYDRA_PUBLIC_URL because it IS the
// issuer. Both now have controls below.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a minimal repo and returns its root.
type fixture struct {
	goMod        string
	files        map[string]string // repo-relative path -> contents
	noChart      bool
	chartEnv     string // rendered env block for the deployment template
	valuesAuth   string // the `auth:` block in values.yaml
	authConfFile string

	// authProfile is the .authprofile content. Defaults to the type the default
	// fixture actually is — it constructs auth.NewVerifier and spends no
	// outbound token — so existing tests stay about the rule they were written
	// for rather than becoming profile tests. Set noAuthProfile to omit it.
	authProfile   string
	noAuthProfile bool
}

func (fx fixture) build(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	goMod := fx.goMod
	if goMod == "" {
		goMod = "module example.com/svc\n\ngo 1.24\n\nrequire github.com/mikelear/leartech-go-common v1.2.0\n"
	}
	write(t, filepath.Join(root, "go.mod"), goMod)

	// Every fixture is an auth consumer unless it overrides main.go.
	if _, ok := fx.files["main.go"]; !ok {
		write(t, filepath.Join(root, "main.go"), `package main

import "github.com/mikelear/leartech-go-common/pkg/auth"

func main() { _, _ = auth.NewVerifier(nil, auth.VerifierConfig{}) }
`)
	}
	for rel, content := range fx.files {
		write(t, filepath.Join(root, rel), content)
	}

	if !fx.noChart {
		dir := filepath.Join(root, "charts", "svc")
		write(t, filepath.Join(dir, "Chart.yaml"), "apiVersion: v2\nname: svc\nversion: 0.1.0\n")

		env := fx.chartEnv
		if env == "" {
			env = "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
				"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n"
		}
		write(t, filepath.Join(dir, "templates", "deployment.yaml"),
			"apiVersion: apps/v1\nkind: Deployment\nspec:\n  template:\n    spec:\n      containers:\n      - name: svc\n        env:\n"+env)

		values := "auth:\n"
		if fx.valuesAuth != "" {
			values = fx.valuesAuth
		}
		write(t, filepath.Join(dir, "values.yaml"), values)
	}

	if fx.authConfFile != "" {
		write(t, filepath.Join(root, ".authconformance"), fx.authConfFile)
	}
	if !fx.noAuthProfile {
		ap := fx.authProfile
		if ap == "" {
			ap = "type: inbound-resource-server\n"
		}
		write(t, filepath.Join(root, ".authprofile"), ap)
	}
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// run executes the checks the way main() does and returns the rules that fired.
func run(t *testing.T, root string) []string {
	t.Helper()
	var r report
	var ev evidence
	ex := loadExemption(root, &r)
	checkGoCommonFloor(root, &r)
	hasOutbound := checkGoSource(root, &r, &ev)
	checkChart(root, ex, hasOutbound, &r)
	prof := loadProfile(root, &r, ev)
	checkProfile(prof, ev, &r)

	var rules []string
	for _, f := range r.findings {
		rules = append(rules, f.rule)
	}
	// Mirror main()'s self-check: a run that examined nothing is a failure, not
	// a pass, and the tests must see that too.
	if r.goFiles == 0 {
		rules = append(rules, "self-check")
	}
	return rules
}

func has(rules []string, rule string) bool {
	for _, r := range rules {
		if r == rule {
			return true
		}
	}
	return false
}

// ── the control: a conforming service must produce nothing ──────────────────

func TestConformingServicePasses(t *testing.T) {
	rules := run(t, fixture{}.build(t))
	if len(rules) != 0 {
		t.Fatalf("a conforming fixture produced findings %v — every other test here is "+
			"meaningless if the baseline fires", rules)
	}
}

// ── go-common floor ─────────────────────────────────────────────────────────

func TestGoCommonBelowFloorFails(t *testing.T) {
	root := fixture{goMod: "module example.com/svc\n\ngo 1.24\n\nrequire github.com/mikelear/leartech-go-common v1.1.0\n"}.build(t)
	if !has(run(t, root), "go-common-floor") {
		t.Fatal("v1.1.0 accepted — below v1.2.0 the audience check is conditional")
	}
}

func TestGoCommonAtFloorPasses(t *testing.T) {
	root := fixture{goMod: "module example.com/svc\n\ngo 1.24\n\nrequire github.com/mikelear/leartech-go-common v1.2.0\n"}.build(t)
	if has(run(t, root), "go-common-floor") {
		t.Fatal("v1.2.0 rejected — the floor is inclusive")
	}
}

// ── Verifier for inbound ────────────────────────────────────────────────────

func TestServiceClientWithoutOutboundLegFails(t *testing.T) {
	root := fixture{files: map[string]string{"main.go": `package main

import "github.com/mikelear/leartech-go-common/pkg/auth"

func main() { _, _ = auth.NewServiceClient(nil, auth.Config{}) }
`}}.build(t)
	if !has(run(t, root), "verifier-for-inbound") {
		t.Fatal("ServiceClient with no outbound call accepted — that is the plan-api shape")
	}
}

// The control that keeps the rule honest: a service that GENUINELY mints
// outbound tokens is entitled to the dual-role client.
func TestServiceClientWithOutboundLegPasses(t *testing.T) {
	root := fixture{files: map[string]string{"main.go": `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`}}.build(t)
	if has(run(t, root), "verifier-for-inbound") {
		t.Fatal("a service that spends an outbound token was flagged — ServiceClient is correct there")
	}
}

// ── disable flags ───────────────────────────────────────────────────────────

func TestDisableFlagInStructTagFails(t *testing.T) {
	root := fixture{files: map[string]string{"config.go": `package main

type Config struct {
	AuthEnabled bool ` + "`envconfig:\"AUTH_ENABLED\" default:\"true\"`" + `
}
`}}.build(t)
	if !has(run(t, root), "no-disable-flag") {
		t.Fatal("AUTH_ENABLED bound via a struct tag accepted")
	}
}

func TestDisableFlagViaGetenvFails(t *testing.T) {
	root := fixture{files: map[string]string{"config.go": `package main

import "os"

func authRequired() string { return os.Getenv("AUTH_REQUIRED") }
`}}.build(t)
	if !has(run(t, root), "no-disable-flag") {
		t.Fatal("AUTH_REQUIRED read via os.Getenv accepted")
	}
}

// THE CONTROL THAT MATTERS. Several repos keep an inert-vars list naming these
// exact strings in order to WARN that they are ignored. Flagging that is
// flagging the fix, and a gate that punishes the remedy gets switched off.
func TestNamingADisableFlagInAnInertListPasses(t *testing.T) {
	root := fixture{files: map[string]string{"config.go": `package main

import "os"

var inertAuthEnv = []string{"AUTH_ENABLED", "AUTH_REQUIRED", "LEARTECH_AUTH_REQUIRED"}

func InertAuthEnvSet() []string {
	var set []string
	for _, k := range inertAuthEnv {
		if os.Getenv(k) != "" {
			set = append(set, k)
		}
	}
	return set
}
`}}.build(t)
	if has(run(t, root), "no-disable-flag") {
		t.Fatal("an inert-vars list was flagged — that list REPORTS the variables as " +
			"ignored, which is the opposite of honouring them")
	}
}

func TestDisableFlagInChartValuesFails(t *testing.T) {
	root := fixture{valuesAuth: "auth:\n  enabled: true\n  audience: x\n"}.build(t)
	if !has(run(t, root), "no-disable-flag") {
		t.Fatal("auth.enabled in values.yaml accepted")
	}
}

// ── chart env contract ──────────────────────────────────────────────────────

func TestChartMissingIssuerFails(t *testing.T) {
	root := fixture{chartEnv: "        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n"}.build(t)
	if !has(run(t, root), "chart-env-contract") {
		t.Fatal("chart without LEARTECH_AUTH_ISSUER accepted")
	}
}

func TestChartWithLegacyEnvNameFails(t *testing.T) {
	root := fixture{chartEnv: "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
		"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n" +
		"        - name: AUTH_SERVERURL\n          value: \"z\"\n"}.build(t)
	if !has(run(t, root), "chart-env-contract") {
		t.Fatal("AUTH_SERVERURL accepted — it is the envconfig-derived name no service uses")
	}
}

func TestChartWithClientCredentialsFails(t *testing.T) {
	root := fixture{valuesAuth: "auth:\n  audience: x\n  clientId: svc\n"}.build(t)
	if !has(run(t, root), "verifier-for-inbound") {
		t.Fatal("auth.clientId accepted — a resource server holds no client identity")
	}
}

// ── exemptions ──────────────────────────────────────────────────────────────

// auth-service's real shape: it IS the issuer, so it supplies the value as
// HYDRA_PUBLIC_URL and declares that with a reason.
func TestDeclaredExemptionWithReasonPasses(t *testing.T) {
	root := fixture{
		chartEnv: "        - name: HYDRA_PUBLIC_URL\n          value: \"https://hydra\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
		authConfFile: "issuer-env: HYDRA_PUBLIC_URL\nreason: this service IS the issuer\n",
	}.build(t)
	if rules := run(t, root); len(rules) != 0 {
		t.Fatalf("a justified exemption still failed: %v", rules)
	}
}

func TestExemptionWithoutReasonFails(t *testing.T) {
	root := fixture{
		chartEnv:     "        - name: HYDRA_PUBLIC_URL\n          value: \"https://hydra\"\n        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
		authConfFile: "issuer-env: HYDRA_PUBLIC_URL\n",
	}.build(t)
	if !has(run(t, root), "exemption") {
		t.Fatal("an exemption with no reason was accepted — that is a silent bypass")
	}
}

// An exemption substitutes a NAME. It must not excuse the value being absent.
func TestExemptionNamingAnUnrenderedVarFails(t *testing.T) {
	root := fixture{
		chartEnv:     "        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
		authConfFile: "issuer-env: NOT_RENDERED_ANYWHERE\nreason: testing\n",
	}.build(t)
	if !has(run(t, root), "chart-env-contract") {
		t.Fatal("an exemption naming a variable the chart never renders was accepted")
	}
}

func TestExemptionReasonKeepsItsWrappedLines(t *testing.T) {
	root := fixture{
		chartEnv: "        - name: HYDRA_PUBLIC_URL\n          value: \"https://hydra\"\n        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
		authConfFile: "issuer-env: HYDRA_PUBLIC_URL\n" +
			"reason: first line\n  second line\n  third line\n",
	}.build(t)

	var r report
	ex := loadExemption(root, &r)
	for _, want := range []string{"first line", "second line", "third line"} {
		if !strings.Contains(ex.reason, want) {
			t.Errorf("reason lost %q — got %q. A mangled justification is worse than "+
				"none, because it still looks like one.", want, ex.reason)
		}
	}
}

// ── applicability ───────────────────────────────────────────────────────────

func TestRepoWithoutAChartIsSkipped(t *testing.T) {
	root := fixture{noChart: true}.build(t)
	if ok, why := applicable(root); ok {
		t.Fatalf("a chart-less repo was enforced (%s) — libraries and CLIs are not deployed services", why)
	}
}

func TestChartRepoWithoutGoCommonAuthIsSkipped(t *testing.T) {
	root := fixture{files: map[string]string{"main.go": "package main\n\nfunc main() {}\n"}}.build(t)
	if ok, why := applicable(root); ok {
		t.Fatalf("a non-auth service was enforced (%s)", why)
	}
}

func TestServiceWithChartAndGoCommonAuthIsEnforced(t *testing.T) {
	if ok, _ := applicable(fixture{}.build(t)); !ok {
		t.Fatal("a deployed auth consumer was skipped — the gate would enforce nothing")
	}
}

// A service that genuinely MINTS outbound tokens legitimately carries a client
// identity, and the chart rule must not contradict the code rule.
//
// This case was a FALSE POSITIVE before the rule became role-aware:
// leartech-mcp-servers verifies inbound AND builds per-server s2s clients, and
// the gate told it to delete credentials it actually spends.
func TestChartClientCredentialsAllowedWhenAServiceMintsTokens(t *testing.T) {
	root := fixture{
		files: map[string]string{"main.go": `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	// Dual-role: validates inbound AND mints for outbound. The verifier is
	// here because the scenario this fixture describes (mcp-servers) does
	// both, and the profile cross-check needs the inbound half to be real
	// rather than implied by the chart.
	_, _ = auth.NewVerifier(nil, auth.VerifierConfig{})
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`},
		authProfile: "type: dual-role\n",
		chartEnv: "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n" +
			"        - name: LEARTECH_AUTH_CLIENT_ID\n          value: \"svc\"\n",
		valuesAuth: "auth:\n  audience: x\n  clientId: svc\n",
	}.build(t)

	if rules := run(t, root); len(rules) != 0 {
		t.Fatalf("a service with a real outbound leg was flagged for holding a client identity: %v", rules)
	}
}

// The same chart WITHOUT an outbound leg must still fail — otherwise the
// exemption above would excuse every service.
func TestChartClientCredentialsRefusedWhenNothingMintsTokens(t *testing.T) {
	root := fixture{
		chartEnv: "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n" +
			"        - name: LEARTECH_AUTH_CLIENT_ID\n          value: \"svc\"\n",
	}.build(t)

	if !has(run(t, root), "verifier-for-inbound") {
		t.Fatal("a pure resource server declaring LEARTECH_AUTH_CLIENT_ID was accepted")
	}
}

// LEARTECH_AUTH_SERVER_URL is go-common's TOKEN ENDPOINT (Config.ServerURL),
// not the issuer. A minting service needs it; a resource server must not carry
// it, because it reads like the issuer knob and is not one.
func TestServerURLAllowedWhenAServiceMintsTokens(t *testing.T) {
	root := fixture{
		files: map[string]string{"main.go": `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	// Dual-role: validates inbound AND mints for outbound. The verifier is
	// here because the scenario this fixture describes (mcp-servers) does
	// both, and the profile cross-check needs the inbound half to be real
	// rather than implied by the chart.
	_, _ = auth.NewVerifier(nil, auth.VerifierConfig{})
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`},
		authProfile: "type: dual-role\n",
		chartEnv: "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n" +
			"        - name: LEARTECH_AUTH_SERVER_URL\n          value: \"https://hydra\"\n",
	}.build(t)
	if rules := run(t, root); len(rules) != 0 {
		t.Fatalf("a minting service was flagged for declaring its token endpoint: %v", rules)
	}
}

func TestServerURLRefusedForAPureResourceServer(t *testing.T) {
	root := fixture{
		chartEnv: "        - name: LEARTECH_AUTH_ISSUER\n          value: \"x\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n" +
			"        - name: LEARTECH_AUTH_SERVER_URL\n          value: \"https://hydra\"\n",
	}.build(t)
	if !has(run(t, root), "verifier-for-inbound") {
		t.Fatal("a resource server declaring LEARTECH_AUTH_SERVER_URL was accepted — it is a decoy that reads like the issuer")
	}
}

// A DUAL-ROLE service satisfies the issuer requirement with
// LEARTECH_AUTH_SERVER_URL, because auth.Config has no separate Issuer field —
// ServerURL is both the token endpoint and the JWKS base.
//
// This was a FALSE POSITIVE before: leartech-maestro-service mints tokens for
// its notification client and was told to add a variable go-common does not
// read in that role.
func TestServerURLSatisfiesTheIssuerForAMintingService(t *testing.T) {
	root := fixture{
		files: map[string]string{"main.go": `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	// Dual-role: validates inbound AND mints for outbound. The verifier is
	// here because the scenario this fixture describes (mcp-servers) does
	// both, and the profile cross-check needs the inbound half to be real
	// rather than implied by the chart.
	_, _ = auth.NewVerifier(nil, auth.VerifierConfig{})
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`},
		authProfile: "type: dual-role\n",
		chartEnv: "        - name: LEARTECH_AUTH_SERVER_URL\n          value: \"https://hydra\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
	}.build(t)
	if rules := run(t, root); len(rules) != 0 {
		t.Fatalf("a minting service was told to add LEARTECH_AUTH_ISSUER: %v", rules)
	}
}

// And a pure resource server still may NOT substitute it — there SERVER_URL is
// the token endpoint it never posts to, and the issuer must be named.
func TestServerURLDoesNotSatisfyTheIssuerForAResourceServer(t *testing.T) {
	root := fixture{
		chartEnv: "        - name: LEARTECH_AUTH_SERVER_URL\n          value: \"https://hydra\"\n" +
			"        - name: LEARTECH_AUTH_AUDIENCE\n          value: \"y\"\n",
	}.build(t)
	if !has(run(t, root), "chart-env-contract") {
		t.Fatal("a resource server substituted SERVER_URL for the issuer and was accepted")
	}
}

// ── no-optional-credential ───────────────────────────────────────────────────

const (
	optionalCredEnv = `        - name: LEARTECH_AUTH_ISSUER
          value: "x"
        - name: LEARTECH_AUTH_AUDIENCE
          value: "y"
        - name: LEARTECH_AUTH_CLIENT_ID
          valueFrom:
            secretKeyRef:
              name: svc-oauth
              key: CLIENT_ID
              optional: true
`
	requiredCredEnv = `        - name: LEARTECH_AUTH_ISSUER
          value: "x"
        - name: LEARTECH_AUTH_AUDIENCE
          value: "y"
        - name: LEARTECH_AUTH_CLIENT_ID
          valueFrom:
            secretKeyRef:
              name: svc-oauth
              key: CLIENT_ID
        - name: LEARTECH_AUTH_CLIENT_SECRET
          valueFrom:
            secretKeyRef:
              name: svc-oauth
              key: CLIENT_SECRET
`
	optionalNonCredEnv = `        - name: LEARTECH_AUTH_ISSUER
          value: "x"
        - name: LEARTECH_AUTH_AUDIENCE
          value: "y"
        - name: REDIS_PASSWORD
          valueFrom:
            secretKeyRef:
              name: svc-redis
              key: password
              optional: true
`
)

func TestOptionalCredentialIsRefused(t *testing.T) {
	rules := run(t, fixture{chartEnv: optionalCredEnv}.build(t))
	if !has(rules, "no-optional-credential") {
		t.Fatalf("optional:true on a credential was allowed.\n\n"+
			"It is the same trade as an AUTH_ENABLED flag, which this tool already "+
			"refuses: the pod starts with an empty credential and fails later at token "+
			"mint, as a 401 attributed to the wrong layer. 19 live mounts carried it "+
			"across 14 services on 2026-09-12, concealing an entire missing identity.\n\n"+
			"rules fired: %v", rules)
	}
}

// The control. Without it the refusal above would pass equally on a rule that
// rejected every credential mount.
func TestRequiredCredentialIsAccepted(t *testing.T) {
	rules := run(t, fixture{chartEnv: requiredCredEnv}.build(t))
	if has(rules, "no-optional-credential") {
		t.Fatalf("a correctly-required credential mount was refused: %v", rules)
	}
}

// optional:true is legitimate on things that genuinely are optional. Scope
// creep here gets the whole rule exempted wholesale.
func TestOptionalIsStillAllowedOnNonCredentials(t *testing.T) {
	rules := run(t, fixture{chartEnv: optionalNonCredEnv}.build(t))
	if has(rules, "no-optional-credential") {
		t.Fatalf("the rule fired on a non-auth secret; it is scoped to auth "+
			"credentials on purpose: %v", rules)
	}
}

// The comment a chart author writes when REMOVING the flag must not re-trip it,
// or every chart documenting the decision fails.
func TestCommentMentioningTheFlagDoesNotTrip(t *testing.T) {
	env := `        - name: LEARTECH_AUTH_ISSUER
          value: "x"
        - name: LEARTECH_AUTH_AUDIENCE
          value: "y"
        # NO optional: true here, deliberately — a missing secret must fail the
        # pod at start rather than at first token mint.
        - name: LEARTECH_AUTH_CLIENT_SECRET
          valueFrom:
            secretKeyRef:
              name: svc-oauth
              key: CLIENT_SECRET
`
	rules := run(t, fixture{chartEnv: env}.build(t))
	if has(rules, "no-optional-credential") {
		t.Fatalf("a comment describing the removed flag tripped the rule: %v", rules)
	}
}
