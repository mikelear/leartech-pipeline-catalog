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
	ex := loadExemption(root, &r)
	checkGoCommonFloor(root, &r)
	checkGoSource(root, &r)
	checkChart(root, ex, &r)

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
