package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The setup menu.
//
// WHY A MENU AND NOT SCRIPTS. Every repo with an end2end pack used to write its own
// setup: `curl` at the Hydra admin API, `kubectl exec` into a sibling Postgres, a
// script that flips a fixture user's role. Across the estate that is 72 pack scripts,
// of which the setup half is a dozen near-copies of the same three procedures, each at
// a different age — the same problem the runner consolidation fixed, one layer down.
//
// It is worse than duplication, because setup is the half that can corrupt a shared
// environment. `leartech-auth-service/end2end/00-reset-staging-fixtures.sh` exists
// because a sibling repo's suite flipped `test@` to tenant_admin and could not restore
// it, which reddened a different repo's gate until a redeploy re-seeded it. The header
// of that script says it plainly: "staging is a persistent, SHARED environment".
//
// So the catalog provides the verbs, a repo names the verb and its parameters, and
// nothing else. A verb is idempotent by construction — that is the property that makes
// it safe to run against staging, where the Observer runs the same pack — and a step
// that is not expressible as a verb is bespoke: declaration-only, and scoped to a
// preview.
// preview-only (see Bespoke in contract.go).

// SetupVerb names one provided procedure. The set is closed: a name not in this list
// is a finding, because "the repo brought its own" is what the menu replaces.
type SetupVerb string

const (
	// VerbHydraClient registers an OAuth2 client and asserts it reads back.
	// Idempotent: an existing client with the same id is updated, not duplicated.
	VerbHydraClient SetupVerb = "hydra-client"
	// VerbHydraSecret sets a client secret from GSM and asserts the client accepts it.
	VerbHydraSecret SetupVerb = "hydra-secret"
	// VerbAuthUser creates or asserts the role of a fixture user.
	VerbAuthUser SetupVerb = "auth-user"
	// VerbScope declares a capability scope and asserts the fleet's vocabulary agrees.
	// Vocabulary, not enforcement: go/scopedrift already checks the pair.
	VerbScope SetupVerb = "scope"
	// VerbAudience declares an audience and asserts a token can be minted carrying it.
	VerbAudience SetupVerb = "audience"
	// VerbProbe asserts an endpoint answers with a status. The only verb that needs no
	// cluster API: it covers what a chart's own readiness has no opinion about.
	VerbProbe SetupVerb = "probe"
)

// menu lists the verbs this release can both validate and run. A verb in the constants
// above but not here is declarable and validated, and running it says so — better than
// a half-written verb that looks finished.
var menu = map[SetupVerb]bool{
	VerbHydraClient: true,
	VerbProbe:       true,
}

// SetupWhere is which environment a step is for. It exists because the two are not the
// same problem: a preview is ephemeral and provisioned per PR, staging is persistent and
// shared and is what the Observer runs against.
type SetupWhere string

const (
	// WherePreview runs only in a preview (the default).
	WherePreview SetupWhere = "preview"
	// WhereStaging runs only against staging.
	WhereStaging SetupWhere = "staging"
	// WhereBoth runs in either. Only verbs that are idempotent appear here, and every
	// menu verb is idempotent by construction.
	WhereBoth SetupWhere = "both"
)

// DeclaredSetup is one step as written in end2end/preview.yaml.
type DeclaredSetup struct {
	Verb  string `yaml:"verb"`
	Where string `yaml:"where"`
	// The parameters the verb needs. Declared flat rather than as a map so a typo is a
	// parse error rather than a silently-ignored key, which is the failure mode the
	// whole contract exists to remove.
	Name         string   `yaml:"name"`
	Scopes       []string `yaml:"scopes"`
	Audiences    []string `yaml:"audiences"`
	GrantTypes   []string `yaml:"grantTypes"`
	RedirectURIs []string `yaml:"redirectURIs"`
	Email        string   `yaml:"email"`
	Role         string   `yaml:"role"`
	Password     string   `yaml:"password"`
	URL          string   `yaml:"url"`
	Want         int      `yaml:"want"`
	Note         string   `yaml:"note"`
}

// Setup is a validated step: the verb is known, `where` resolved, and the parameters
// its verb requires are present.
type Setup struct {
	Verb         SetupVerb
	Where        SetupWhere
	Name         string
	Scopes       []string
	Audiences    []string
	GrantTypes   []string
	RedirectURIs []string
	Email        string
	Role         string
	Password     string
	URL          string
	Want         int
	Note         string
}

// Bespoke is a declared exception: a script the menu does not cover, a preview scope,
// and a reason a reviewer can weigh. Declaring it opts out of the menu rule for that
// file
// and nothing else, and the check prints it so a repo does not accumulate three.
type Bespoke struct {
	Script string `yaml:"script"`
	Reason string `yaml:"reason"`
	// Where is honoured only for preview. A bespoke script against staging is refused:
	// staging is shared, and the one script in the estate that repairs shared-state
	// damage exists because another repo's bespoke setup caused it.
	Where string `yaml:"where"`
}

// resolveSetup validates one declared step and returns the concrete form.
func resolveSetup(d DeclaredSetup) (Setup, []string) {
	var findings []string
	s := Setup{
		Verb:         SetupVerb(strings.TrimSpace(d.Verb)),
		Name:         d.Name,
		Scopes:       d.Scopes,
		Audiences:    d.Audiences,
		GrantTypes:   d.GrantTypes,
		RedirectURIs: d.RedirectURIs,
		Email:        d.Email,
		Role:         d.Role,
		Password:     d.Password,
		URL:          d.URL,
		Want:         d.Want,
		Note:         d.Note,
	}

	switch SetupWhere(strings.TrimSpace(d.Where)) {
	case "":
		s.Where = WherePreview
	case WherePreview, WhereStaging, WhereBoth:
		s.Where = SetupWhere(strings.TrimSpace(d.Where))
	default:
		s.Where = WherePreview
		findings = append(findings, fmt.Sprintf(
			"setup %q: where %q is not one of preview|staging|both, so it would be treated "+
				"as preview and then not run where it was wanted.", d.Verb, d.Where))
	}

	switch s.Verb {
	case VerbHydraClient:
		if s.Name == "" {
			findings = append(findings, "setup hydra-client: `name` is required (it is the client id).")
		}
		if len(s.Scopes) == 0 {
			findings = append(findings, fmt.Sprintf(
				"setup hydra-client %q: `scopes` is empty. A client granted no scopes cannot "+
					"exercise a scope-gated route, which is what an end2end suite is for here.", s.Name))
		}
		if len(s.Audiences) == 0 {
			findings = append(findings, fmt.Sprintf(
				"setup hydra-client %q: `audiences` is empty. An audience is what a resource "+
					"server refuses on, so a client without one cannot test the refusal that "+
					"protects it.", s.Name))
		}
		if len(s.RedirectURIs) == 0 {
			findings = append(findings, fmt.Sprintf(
				"setup hydra-client %q: `redirectURIs` is empty; an authorization_code client "+
					"needs one and Hydra will reject the grant.", s.Name))
		}
		for i, u := range s.RedirectURIs {
			if strings.Contains(u, "${PREVIEW_URL}") || strings.Contains(u, "${NAMESPACE}") ||
				strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"setup hydra-client %q: redirectURIs[%d] %q is neither a URL nor a template "+
					"(${PREVIEW_URL} / ${NAMESPACE}); Hydra will register it and then never "+
					"match it.", s.Name, i, u))
		}
	case VerbProbe:
		if strings.TrimSpace(s.URL) == "" {
			findings = append(findings, "setup probe: `url` is required.")
		}
		if s.Want < 100 || s.Want > 599 {
			findings = append(findings, fmt.Sprintf(
				"setup probe %q: `want` must be an HTTP status (got %d).", s.URL, s.Want))
		}
	case VerbHydraSecret, VerbAuthUser, VerbScope, VerbAudience:
		// Declarable and validated structurally; not yet runnable (see menu). Saying so
		// here means `e2e setup --apply` can fail with the truth rather than a panic.
	default:
		findings = append(findings, fmt.Sprintf(
			"setup verb %q is not on the catalog's menu (%s). The menu is the point: a "+
				"bespoke setup procedure is what corrupted a shared staging fixture and cost "+
				"another repo a red gate for a day. If the procedure is genuinely not a verb, "+
				"declare it under `bespoke:` with a reason and it stays preview-only.",
			d.Verb, menuNames()))
	}

	// A staging step runs against the persistent environment the Observer uses, so it
	// has to be safe to run repeatedly and safe alongside a sibling repo's run. Every
	// menu verb is idempotent; nothing else is allowed there.
	if s.Where == WhereStaging || s.Where == WhereBoth {
		if !menu[s.Verb] {
			findings = append(findings, fmt.Sprintf(
				"setup %q declares where: %s, but it is not yet a runnable menu verb. A "+
					"staging step runs on every Observer dispatch against a SHARED environment; "+
					"an unproven procedure there is how a fixture gets poisoned. Keep it "+
					"where: preview until the verb lands.", s.Verb, s.Where))
		}
	}
	return s, findings
}

// menuNames renders the closed set for a finding.
func menuNames() string {
	names := make([]string, 0, len(VerbHydraClient)+7)
	for _, v := range []SetupVerb{VerbHydraClient, VerbHydraSecret, VerbAuthUser, VerbScope, VerbAudience, VerbProbe} {
		names = append(names, string(v))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// MenuHelp is the text a consumer gets when they ask what they may declare, and when a
// finding tells them their verb is not on it. It is the documentation for the menu, kept
// next to the code, so the list above is the only place it is stated.
const MenuHelp = `The catalog's setup menu. Declare one entry per procedure under preview.setup in
end2end/preview.yaml; do not write your own script.

  verb: hydra-client      register an OAuth2 client. idempotent (409 -> update).
      name: <client id>   scopes: [...]   audiences: [...]
      grantTypes: [...]   redirectURIs: ["${PREVIEW_URL}/callback"]

  verb: probe             assert an endpoint answers. the one thing a chart's own
      url: <url>          readinessProbe cannot cover, e.g. a discovery document.
      want: 200           note: <why this URL matters>

  verb: hydra-secret      set a client secret from GSM and assert the client accepts it.
  verb: auth-user         create or assert a fixture user's role (email, password, role).
  verb: scope             declare a capability scope so the fleet's vocabulary agrees.
  verb: audience          declare an audience and assert a token can be minted for it.

Every verb is idempotent, which is what makes ` + "`where: staging`" + ` safe: the Observer runs
the same pack against staging, which is persistent and shared.

A procedure the menu does not cover is declared under ` + "`bespoke:`" + ` with a reason, and
runs in a preview only.`

// setupPlan renders what would happen, for `e2e setup --plan`. A plan a consumer can
// read is the difference between "the pipeline did something" and knowing what.
func setupPlan(steps []Setup, where SetupWhere) []string {
	var out []string
	for _, s := range steps {
		if s.Where != where && s.Where != WhereBoth {
			continue
		}
		line := fmt.Sprintf("%-14s %s", s.Verb, s.describe())
		if !menu[s.Verb] {
			line += "   [not runnable yet: declare it, keep your script until it is]"
		}
		out = append(out, line)
	}
	return out
}

func (s Setup) describe() string {
	switch s.Verb {
	case VerbHydraClient:
		return fmt.Sprintf("client %q scopes=%v audiences=%v redirects=%v",
			s.Name, s.Scopes, s.Audiences, s.RedirectURIs)
	case VerbProbe:
		return fmt.Sprintf("%s -> %d%s", s.URL, s.Want, noteSuffix(s.Note))
	case VerbAuthUser:
		return fmt.Sprintf("user %s role=%s", s.Email, s.Role)
	case VerbHydraSecret:
		return fmt.Sprintf("secret for client %q from GSM", s.Name)
	case VerbScope:
		return fmt.Sprintf("scope %v", s.Scopes)
	case VerbAudience:
		return fmt.Sprintf("audience %v", s.Audiences)
	}
	return s.URL
}

// expandSetup resolves the runtime templates in a step against the run's namespace and
// preview URL, so one declaration works in every preview.
func expandSetup(s Setup, namespace, previewURL string) Setup {
	repl := func(v string) string {
		v = strings.ReplaceAll(v, "${NAMESPACE}", namespace)
		v = strings.ReplaceAll(v, "${PREVIEW_URL}", strings.TrimSuffix(previewURL, "/"))
		return v
	}
	out := s
	out.URL = repl(s.URL)
	out.RedirectURIs = nil
	for _, u := range s.RedirectURIs {
		out.RedirectURIs = append(out.RedirectURIs, repl(u))
	}
	return out
}

// ── running the verbs that are runnable ────────────────────────────────────

// setupDeps is what a verb needs from the outside world. Interfaces, so the verbs are
// testable without a cluster and without a Hydra.
type setupDeps struct {
	// do issues a request and returns the status and body. It exists rather than a bare
	// GET because registering a client is a PUT with a body.
	do func(ctx context.Context, method, url string, body []byte) (int, []byte, error)
	// hydraAdminURL returns the admin endpoint for a namespace.
	hydraAdminURL func(ctx context.Context, namespace string) string
}

// applySetup runs the runnable verbs and reports the rest. A declarable-but-
// unimplemented verb is an error naming itself, because a quiet no-op here is a suite
// that passes without its fixtures.
func applySetup(ctx context.Context, steps []Setup, where SetupWhere, namespace, previewURL string, d setupDeps) (int, error) {
	ran := 0
	for _, raw := range steps {
		if raw.Where != where && raw.Where != WhereBoth {
			continue
		}
		s := expandSetup(raw, namespace, previewURL)
		if !menu[s.Verb] {
			return ran, fmt.Errorf(
				"setup %q is declared but not runnable yet (declarable verbs: %s). Keep the "+
					"bespoke script and declare it under `bespoke:` until this verb lands — a "+
					"suite that silently ran without its fixture is worse than one that failed",
				s.Verb, menuNames())
		}
		switch s.Verb {
		case VerbHydraClient:
			if err := applyHydraClient(ctx, s, namespace, d); err != nil {
				return ran, err
			}
			ran++
		case VerbProbe:
			code, _, err := d.do(ctx, "GET", s.URL, nil)
			if err != nil {
				return ran, fmt.Errorf("setup probe %s: %w", s.URL, err)
			}
			if code != s.Want {
				return ran, fmt.Errorf("setup probe %s returned %d, want %d%s", s.URL, code, s.Want, noteSuffix(s.Note))
			}
			ran++
		}
	}
	return ran, nil
}

// applyHydraClient registers a client and asserts it reads back with the scopes it was
// given. The read-back is the point: Hydra accepts a PUT for a client whose grant types
// it will then refuse, and the rejection only appears at the token request, in a
// different repo's suite.
func applyHydraClient(ctx context.Context, s Setup, namespace string, d setupDeps) error {
	base := d.hydraAdminURL(ctx, namespace)
	if base == "" {
		return fmt.Errorf("setup hydra-client %q: no Hydra admin endpoint in namespace %s. "+
			"The preview must deploy its own issuer (leartech-preview-infrastructure); a "+
			"preview that borrows staging's issuer cannot be set up without touching staging",
			s.Name, namespace)
	}
	payload := map[string]any{
		"client_id":                  s.Name,
		"client_name":                s.Name,
		"grant_types":                s.GrantTypes,
		"response_types":             []string{"code"},
		"scope":                      strings.Join(s.Scopes, " "),
		"audience":                   s.Audiences,
		"redirect_uris":              s.RedirectURIs,
		"token_endpoint_auth_method": "client_secret_basic",
	}
	if len(s.GrantTypes) == 0 {
		payload["grant_types"] = []string{"authorization_code", "refresh_token"}
	}
	body, _ := json.Marshal(payload)
	url := strings.TrimSuffix(base, "/") + "/admin/clients/" + s.Name
	code, respBody, err := d.do(ctx, "PUT", url, body)
	if err != nil {
		return fmt.Errorf("setup hydra-client %q: %w", s.Name, err)
	}
	if code < 200 || code > 299 {
		return fmt.Errorf("setup hydra-client %q: PUT %s returned %d: %s",
			s.Name, url, code, truncate(string(respBody), 300))
	}

	// Read back. A client that was written and not read back is the shape of "seeded"
	// that later fails in a suite three repos away.
	code, got, err := d.do(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("setup hydra-client %q: read-back: %w", s.Name, err)
	}
	if code != 200 {
		return fmt.Errorf("setup hydra-client %q: read-back returned %d", s.Name, code)
	}
	var read struct {
		Scope    string   `json:"scope"`
		Audience []string `json:"audience"`
	}
	if err := json.Unmarshal(got, &read); err != nil {
		return fmt.Errorf("setup hydra-client %q: read-back is not JSON: %w", s.Name, err)
	}
	have := map[string]bool{}
	for _, sc := range strings.Fields(read.Scope) {
		have[sc] = true
	}
	for _, want := range s.Scopes {
		if !have[want] {
			return fmt.Errorf(
				"setup hydra-client %q: asked for scope %q and Hydra did not store it "+
					"(has %q). An unrequestable scope is a route nobody can test",
				s.Name, want, read.Scope)
		}
	}
	aud := map[string]bool{}
	for _, a := range read.Audience {
		aud[a] = true
	}
	for _, want := range s.Audiences {
		if !aud[want] {
			return fmt.Errorf(
				"setup hydra-client %q: asked for audience %q and Hydra did not store it "+
					"(has %v). The refusal a resource server makes is the thing being tested, "+
					"so a missing audience silently removes the test",
				s.Name, want, read.Audience)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func runSetup(args []string) int {
	fs := flag.NewFlagSet("e2e setup", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository root")
	apply := fs.Bool("apply", false, "run the plan instead of printing it")
	namespace := fs.String("namespace", os.Getenv("PREVIEW_NAMESPACE"), "preview namespace")
	previewURL := fs.String("preview-url", os.Getenv("PREVIEW_URL"), "the preview's public URL")
	where := fs.String("where", defaultWhere(), "preview or staging")
	if err := fs.Parse(args); err != nil {
		return 64
	}

	c, findings := LoadContract(*dir)
	if c == nil {
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, f)
		}
		return 2
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", f)
	}

	plan := setupPlan(c.Setup, SetupWhere(*where))
	if len(plan) == 0 {
		fmt.Printf("==> nothing to set up for %s (no setup entries apply)\n", *where)
		return 0
	}
	fmt.Printf("==> setup plan for %s:\n", *where)
	for _, l := range plan {
		fmt.Printf("  %s\n", l)
	}
	if !*apply {
		fmt.Println("\n(plan only; pass --apply to run it)")
		return 0
	}

	deps := setupDeps{do: realDo, hydraAdminURL: hydraAdminFor}
	ran, err := applySetup(context.Background(), c.Setup, SetupWhere(*where), *namespace, *previewURL, deps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "==> setup FAILED after %d step(s): %v\n", ran, err)
		return 1
	}
	fmt.Printf("==> setup complete: %d step(s)\n", ran)
	return 0
}

// defaultWhere picks staging when the Observer dispatched the run (it sets STAGING_URL
// and only that), and preview otherwise. One pack, two environments, no flag needed.
func defaultWhere() string {
	if os.Getenv("STAGING_URL") != "" {
		return string(WhereStaging)
	}
	return string(WherePreview)
}
