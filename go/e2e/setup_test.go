package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The menu's behaviour, tested the way the estate learned it: a staging run against a
// shared environment, and a preview whose issuer is its own.

func setupStub(bodies map[string]string, codes map[string]int) (setupDeps, *[]string) {
	var calls []string
	d := setupDeps{
		do: func(ctx context.Context, method, url string, body []byte) (int, []byte, error) {
			calls = append(calls, method+" "+url)
			key := method + " " + url
			if c, ok := codes[key]; ok {
				body := bodies[key]
				if body == "" {
					body = bodies[url]
				}
				return c, []byte(body), nil
			}
			if c, ok := codes[url]; ok {
				return c, []byte(bodies[url]), nil
			}
			return 0, nil, errors.New("not stubbed: " + key)
		},
		hydraAdminURL: func(ctx context.Context, ns string) string {
			if ns == "" {
				return ""
			}
			return "http://preview-hydra-admin." + ns + ".svc:4445"
		},
	}
	return d, &calls
}

func hydraClientStep() Setup {
	return Setup{
		Verb:         VerbHydraClient,
		Where:        WherePreview,
		Name:         "thing-e2e",
		Scopes:       []string{"openid", "thing:read"},
		Audiences:    []string{"leartech-thing"},
		RedirectURIs: []string{"https://thing-pr1.example/callback"},
	}
}

// A client is written, then READ BACK, and the read-back is what decides: Hydra accepts
// a PUT for a client it will refuse at the token request, in a different repo's suite.
func TestApplyHydraClientAssertsTheReadBack(t *testing.T) {
	url := "http://preview-hydra-admin.jx-pr1.svc:4445/admin/clients/thing-e2e"
	d, calls := setupStub(
		map[string]string{url: `{"scope":"openid thing:read","audience":["leartech-thing"]}`},
		map[string]int{"PUT " + url: 201, "GET " + url: 200})

	ran, err := applySetup(context.Background(), []Setup{hydraClientStep()}, WherePreview, "jx-pr1", "", d)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if ran != 1 {
		t.Errorf("ran %d steps, want 1", ran)
	}
	if len(*calls) != 2 || !strings.HasPrefix((*calls)[0], "PUT") || !strings.HasPrefix((*calls)[1], "GET") {
		t.Errorf("calls = %v, want a PUT then a GET", *calls)
	}
}

// A scope that did not stick means a route nobody can test, so it is an error rather
// than a warning.
func TestApplyHydraClientFailsWhenAScopeDidNotStick(t *testing.T) {
	url := "http://preview-hydra-admin.jx-pr1.svc:4445/admin/clients/thing-e2e"
	d, _ := setupStub(
		map[string]string{url: `{"scope":"openid","audience":["leartech-thing"]}`},
		map[string]int{"PUT " + url: 201, "GET " + url: 200})

	_, err := applySetup(context.Background(), []Setup{hydraClientStep()}, WherePreview, "jx-pr1", "", d)
	if err == nil || !strings.Contains(err.Error(), "thing:read") {
		t.Fatalf("err = %v, want it to name the scope that did not stick", err)
	}
}

func TestApplyHydraClientFailsWhenAnAudienceDidNotStick(t *testing.T) {
	url := "http://preview-hydra-admin.jx-pr1.svc:4445/admin/clients/thing-e2e"
	d, _ := setupStub(
		map[string]string{url: `{"scope":"openid thing:read","audience":[]}`},
		map[string]int{"PUT " + url: 201, "GET " + url: 200})

	_, err := applySetup(context.Background(), []Setup{hydraClientStep()}, WherePreview, "jx-pr1", "", d)
	if err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("err = %v, want it to name the missing audience", err)
	}
}

// A preview with no issuer of its own cannot be set up without touching staging, which
// is the thing the standard forbids. Say that, not "connection refused".
func TestApplyHydraClientNamesTheMissingIssuer(t *testing.T) {
	d, _ := setupStub(nil, nil)
	_, err := applySetup(context.Background(), []Setup{hydraClientStep()}, WherePreview, "", "", d)
	if err == nil || !strings.Contains(err.Error(), "own issuer") {
		t.Fatalf("err = %v, want it to name the missing issuer", err)
	}
}

// A declarable-but-unimplemented verb is an error naming itself: a quiet no-op is a
// suite that passes without its fixtures.
func TestApplySetupRefusesAnUnrunnableVerb(t *testing.T) {
	d, _ := setupStub(nil, nil)
	_, err := applySetup(context.Background(),
		[]Setup{{Verb: VerbAuthUser, Where: WherePreview, Email: "a@b", Role: "admin"}},
		WherePreview, "jx-pr1", "", d)
	if err == nil || !strings.Contains(err.Error(), "not runnable yet") {
		t.Fatalf("err = %v, want it to say the verb is not runnable", err)
	}
}

// `where` is honoured: a preview run does not execute a staging step, and the other way
// round. This is what makes one declaration usable by both lanes.
func TestApplySetupHonoursWhere(t *testing.T) {
	d, calls := setupStub(
		map[string]string{"http://x.${NAMESPACE}.svc/health": `{}`},
		map[string]int{"GET http://x.jx-pr1.svc/health": 200})
	steps := []Setup{
		{Verb: VerbProbe, Where: WhereStaging, URL: "http://staging-only/health", Want: 200},
		{Verb: VerbProbe, Where: WhereBoth, URL: "http://x.${NAMESPACE}.svc/health", Want: 200},
	}
	ran, err := applySetup(context.Background(), steps, WherePreview, "jx-pr1", "", d)
	if err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if ran != 1 {
		t.Errorf("ran %d, want only the where: both step", ran)
	}
	for _, c := range *calls {
		if strings.Contains(c, "staging-only") {
			t.Errorf("ran a staging step in a preview: %s", c)
		}
	}
}

// One declaration, every preview: the templates resolve against the run.
func TestExpandSetupResolvesTemplates(t *testing.T) {
	s := expandSetup(Setup{RedirectURIs: []string{"${PREVIEW_URL}/cb"}, URL: "http://a.${NAMESPACE}.svc"},
		"jx-pr9", "https://thing-pr9.example")
	if s.URL != "http://a.jx-pr9.svc" {
		t.Errorf("URL = %q", s.URL)
	}
	if len(s.RedirectURIs) != 1 || s.RedirectURIs[0] != "https://thing-pr9.example/cb" {
		t.Errorf("redirectURIs = %v", s.RedirectURIs)
	}
}

// A plan is readable, and marks what cannot run yet, so a consumer is never surprised by
// a step that did nothing.
func TestSetupPlanMarksUnrunnableVerbs(t *testing.T) {
	plan := setupPlan([]Setup{
		{Verb: VerbProbe, Where: WhereBoth, URL: "http://x/health", Want: 200},
		{Verb: VerbAuthUser, Where: WhereBoth, Email: "a@b", Role: "admin"},
	}, WhereStaging)
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "not runnable yet") {
		t.Errorf("plan does not mark the unrunnable verb:\n%s", joined)
	}
	if !strings.Contains(joined, "probe") {
		t.Errorf("plan omitted the runnable verb:\n%s", joined)
	}
}

// The menu is closed, and the help a consumer gets lists exactly what the validator
// accepts — otherwise the two drift and a repo is told its correct verb is unknown.
func TestMenuAndHelpAgree(t *testing.T) {
	for _, v := range []SetupVerb{VerbHydraClient, VerbHydraSecret, VerbAuthUser, VerbScope, VerbAudience, VerbProbe} {
		if !strings.Contains(MenuHelp, string(v)) {
			t.Errorf("MenuHelp does not mention %q", v)
		}
		if _, findings := resolveSetup(DeclaredSetup{Verb: string(v), Name: "n", Scopes: []string{"s"},
			Audiences: []string{"a"}, RedirectURIs: []string{"${PREVIEW_URL}/cb"}, URL: "http://x", Want: 200}); len(findings) > 0 {
			for _, f := range findings {
				if strings.Contains(f, "not on the catalog's menu") {
					t.Errorf("%q is a declared verb but the validator rejects it: %s", v, f)
				}
			}
		}
	}
}

func TestResolveSetupRequiresParams(t *testing.T) {
	cases := []struct {
		name string
		d    DeclaredSetup
		want string
	}{
		{"hydra-client without scopes", DeclaredSetup{Verb: "hydra-client", Name: "x",
			Audiences: []string{"a"}, RedirectURIs: []string{"https://x/cb"}}, "scopes"},
		{"hydra-client without audiences", DeclaredSetup{Verb: "hydra-client", Name: "x",
			Scopes: []string{"s"}, RedirectURIs: []string{"https://x/cb"}}, "audiences"},
		{"hydra-client without redirects", DeclaredSetup{Verb: "hydra-client", Name: "x",
			Scopes: []string{"s"}, Audiences: []string{"a"}}, "redirectURIs"},
		{"probe without url", DeclaredSetup{Verb: "probe", Want: 200}, "url"},
	}
	for _, c := range cases {
		_, findings := resolveSetup(c.d)
		found := false
		for _, f := range findings {
			if strings.Contains(f, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: findings = %v, want one naming %q", c.name, findings, c.want)
		}
	}
}

// A staging step must be a runnable menu verb: every menu verb is idempotent, which is
// what makes it safe on the environment the Observer shares.
func TestResolveSetupRefusesUnprovenStagingStep(t *testing.T) {
	_, _ = json.Marshal(nil)
	_, findings := resolveSetup(DeclaredSetup{Verb: "auth-user", Where: "staging", Email: "a@b", Role: "admin"})
	found := false
	for _, f := range findings {
		if strings.Contains(f, "SHARED environment") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %v, want one about the shared staging environment", findings)
	}
}

// The first real client migrated onto this menu is a browser client: no secret
// (token_endpoint_auth_method "none"), a logout redirect, and eight audiences. A menu
// that cannot express it is a menu nobody migrates to, so the shape is pinned.
func TestApplyHydraClientExpressesABrowserClient(t *testing.T) {
	var sent map[string]any
	d := setupDeps{
		do: func(ctx context.Context, method, u string, body []byte) (int, []byte, error) {
			if method == "PUT" {
				_ = json.Unmarshal(body, &sent)
				return 201, nil, nil
			}
			return 200, []byte(`{"scope":"openid profile email leartechapi offline",` +
				`"audience":["leartech-auth-service"]}`), nil
		},
		hydraAdminURL: func(ctx context.Context, ns string) string {
			return "http://preview-hydra-admin." + ns + ".svc:4445"
		},
	}

	step := Setup{
		Verb:                    VerbHydraClient,
		Where:                   WherePreview,
		Name:                    "frontend-services",
		Scopes:                  []string{"openid", "profile", "email", "leartechapi", "offline"},
		Audiences:               []string{"leartech-auth-service"},
		RedirectURIs:            []string{"https://x-pr1.example/auth/callback"},
		PostLogoutRedirectURIs:  []string{"https://x-pr1.example"},
		TokenEndpointAuthMethod: "none",
	}
	if _, err := applySetup(context.Background(), []Setup{step}, WherePreview, "jx-pr1", "", d); err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if sent["token_endpoint_auth_method"] != "none" {
		t.Errorf("token_endpoint_auth_method = %v, want none: a browser client cannot keep a secret",
			sent["token_endpoint_auth_method"])
	}
	if _, ok := sent["post_logout_redirect_uris"]; !ok {
		t.Error("post_logout_redirect_uris missing from the request; the logout redirect would be dropped in silence")
	}
}

// A server-side client that says nothing gets the safe default.
func TestApplyHydraClientDefaultsToASecret(t *testing.T) {
	var sent map[string]any
	d := setupDeps{
		do: func(ctx context.Context, method, u string, body []byte) (int, []byte, error) {
			if method == "PUT" {
				_ = json.Unmarshal(body, &sent)
				return 201, nil, nil
			}
			return 200, []byte(`{"scope":"openid","audience":["a"]}`), nil
		},
		hydraAdminURL: func(ctx context.Context, ns string) string { return "http://h." + ns + ":4445" },
	}
	step := Setup{Verb: VerbHydraClient, Where: WherePreview, Name: "srv",
		Scopes: []string{"openid"}, Audiences: []string{"a"},
		RedirectURIs: []string{"https://x/cb"}}
	if _, err := applySetup(context.Background(), []Setup{step}, WherePreview, "ns", "", d); err != nil {
		t.Fatalf("applySetup: %v", err)
	}
	if sent["token_endpoint_auth_method"] != "client_secret_basic" {
		t.Errorf("default auth method = %v, want client_secret_basic", sent["token_endpoint_auth_method"])
	}
}
