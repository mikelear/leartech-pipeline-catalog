package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T, s string) Repo {
	t.Helper()
	r, err := ParseRepo(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A repo filter that silently matches nothing is indistinguishable from a forge with no hooks,
// so a malformed name is refused rather than parsed into an empty owner.
//
// proven-by: TestParseRepo_RefusesAMalformedName
func TestParseRepo_RefusesAMalformedName(t *testing.T) {
	for _, bad := range []string{"", "demo", "/demo", "mikelear/", "a/b/c"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) was accepted", bad)
		}
	}
	if r := repo(t, "it-test/lh-test"); r.Owner != "it-test" || r.Name != "lh-test" {
		t.Errorf("parsed %+v", r)
	}
}

// proven-by: TestNewHooks_RefusesAnUnknownKind
func TestNewHooks_RefusesAnUnknownKind(t *testing.T) {
	if _, err := NewHooks(Provider{Name: "x", Kind: "bitbucket"}); err == nil {
		t.Fatal("an unknown kind must be refused, not defaulted")
	}
	if _, err := NewHooks(Provider{Name: "f", Kind: KindGitea}); err == nil {
		t.Fatal("a gitea provider with no server must be refused")
	}
	// GitHub has a sane default base, so no server is fine.
	if _, err := NewHooks(Provider{Name: "g", Kind: KindGitHub}); err != nil {
		t.Fatalf("github with no server: %v", err)
	}
}

// The GitHub driver must send name:"web" and the gitea driver must NOT — a copy-paste driver
// that sends GitHub's shape to Forgejo gets a 422 the caller reads as "already exists", so the
// wrong field fails silently.
//
// proven-by: TestCreateHook_SendsTheProvidersOwnHookType
func TestCreateHook_SendsTheProvidersOwnHookType(t *testing.T) {
	cases := []struct {
		kind     Kind
		base     string
		wantPath string
		wantType string
		forbid   string
	}{
		{KindGitHub, "https://api.github.com", "/repos/mikelear/demo/hooks", "name", "type"},
		{KindGitea, "https://forgejo.example", "/api/v1/repos/mikelear/demo/hooks", "type", "name"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			var body map[string]any
			var path, auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path, auth = r.URL.Path, r.Header.Get("Authorization")
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":3,"config":{"url":"https://l.example/h"},"events":["push"],"active":true}`))
			}))
			defer srv.Close()

			h, err := NewHooks(Provider{Name: "p", Kind: tc.kind, Base: srv.URL, Token: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.CreateHook(context.Background(), repo(t, "mikelear/demo"),
				Spec{URL: "https://l.example/h", Secret: "s3", Events: []string{"push"}, Active: true}); err != nil {
				t.Fatal(err)
			}
			if path != tc.wantPath {
				t.Errorf("path = %q, want %q", path, tc.wantPath)
			}
			if _, ok := body[tc.wantType]; !ok {
				t.Errorf("body has no %q field: %v", tc.wantType, body)
			}
			if _, ok := body[tc.forbid]; ok {
				t.Errorf("body carries %q, which this forge rejects or ignores", tc.forbid)
			}
			cfg, _ := body["config"].(map[string]any)
			if cfg["secret"] != "s3" {
				t.Errorf("config.secret = %v", cfg["secret"])
			}
			wantAuth := "Bearer tok"
			if tc.kind == KindGitea {
				wantAuth = "token tok"
			}
			if auth != wantAuth {
				t.Errorf("auth = %q, want %q", auth, wantAuth)
			}
		})
	}
}

// An unsigned hook omits the secret rather than sending an empty one, which a forge stores and
// then signs with.
func TestCreateHook_OmitsAnEmptySecret(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	h, _ := NewHooks(Provider{Name: "p", Kind: KindGitea, Base: srv.URL})
	if _, err := h.CreateHook(context.Background(), repo(t, "o/r"), Spec{URL: "https://l.example/h"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := body["config"].(map[string]any)
	if _, present := cfg["secret"]; present {
		t.Error("config.secret was sent although the spec has none")
	}
}

// The idempotency rule lives in one function so it is tested without a forge.
//
// proven-by: TestDecide_CoversCreateReplaceAndNone
func TestDecide_CoversCreateReplaceAndNone(t *testing.T) {
	spec := Spec{URL: "https://l.example/h", Events: []string{"push", "pull_request"}, Active: true}
	cases := []struct {
		name string
		have []Webhook
		want bool
		act  action
	}{
		{"absent", nil, true, actionCreate},
		{"identical", []Webhook{{ID: 4, URL: spec.URL, Events: spec.Events, Active: true}}, true, actionNone},
		{"events moved", []Webhook{{ID: 4, URL: spec.URL, Events: []string{"push"}, Active: true}}, true, actionReplace},
		{"deactivated", []Webhook{{ID: 4, URL: spec.URL, Events: spec.Events, Active: false}}, true, actionReplace},
		{"delete removes it", []Webhook{{ID: 4, URL: spec.URL, Events: spec.Events, Active: true}}, false, actionDelete},
		{"delete with nothing there", nil, false, actionNone},
	}
	for _, tc := range cases {
		got, _ := decide(tc.have, spec, tc.want)
		if got != tc.act {
			t.Errorf("%s: decide = %q, want %q", tc.name, got, tc.act)
		}
	}
}

// A trailing slash is the same endpoint: a config edited by hand has one and a forge's stored
// value does not, and treating those as different doubles the hook.
//
// proven-by: TestSameURL_IgnoresATrailingSlash
func TestSameURL_IgnoresATrailingSlash(t *testing.T) {
	if !sameURL("https://L.example/h/", "https://l.example/h") {
		t.Error("a trailing slash and host case must not make two hooks")
	}
	if sameURL("https://l.example/h", "https://l.example/other") {
		t.Error("different paths are different hooks")
	}
}

func TestSameEvents_IgnoresOrder(t *testing.T) {
	if !sameEvents([]string{"a", "b"}, []string{"b", "a"}) {
		t.Error("order must not matter")
	}
	if sameEvents([]string{"a"}, []string{"a", "b"}) {
		t.Error("a subset is not the same set")
	}
}

// An unset ${VAR} is an error naming the variable, not an empty string: an empty credential is
// a 401 that reads like a forge problem, and an empty SECRET registers an unsigned hook the
// forge accepts and Lighthouse then rejects every delivery from.
//
// proven-by: TestExpand_AnUnsetVariableIsAnError
func TestExpand_AnUnsetVariableIsAnError(t *testing.T) {
	env := func(k string) string { return map[string]string{"SET": "v"}[k] }
	if got, err := expand("a-${SET}-b", env); err != nil || got != "a-v-b" {
		t.Fatalf("expand = %q, %v", got, err)
	}
	_, err := expand("${MISSING}", env)
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Fatalf("err = %v, want one naming MISSING", err)
	}
}

func TestLoadConfig_ResolvesReferencesAndRefusesAnUnsetOne(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte(`
providers:
  - name: forgejo
    kind: gitea
    server: https://forgejo.example
    token: ${TOK}
hooks:
  - provider: forgejo
    url: https://lh.example/hook
    secret: ${HMAC}
    events: [push]
    repos: [it-test/lh-test]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(good)
	if err != nil {
		t.Fatal(err)
	}
	env := func(k string) string { return map[string]string{"TOK": "t", "HMAC": "s"}[k] }
	r, err := resolve(c, c.Hooks, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.providers["forgejo"].Token != "t" || r.hooks[0].Secret != "s" {
		t.Fatalf("references were not resolved: %+v", r)
	}

	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte(`
providers:
  - name: forgejo
    kind: gitea
    server: https://forgejo.example
    token: ${TOK}
hooks:
  - provider: forgejo
    url: https://lh.example/hook
    events: [push]
    repos: [it-test/lh-test]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(bad)
	if err != nil {
		t.Fatalf("an unset reference must not fail the LOAD, only the resolve: %v", err)
	}
	if _, err := resolve(c, c.Hooks, func(string) string { return "" }, nil); err == nil {
		t.Fatal("an unset token must fail the resolve")
	}
}

// proven-by: TestValidate_RefusesAHalfWorkingConfig
func TestValidate_RefusesAHalfWorkingConfig(t *testing.T) {
	p := []ProviderConfig{{Name: "f", Kind: "gitea", Server: "https://f"}}
	h := func(mut func(*HookConfig)) []HookConfig {
		hk := HookConfig{Provider: "f", URL: "https://l/h", Events: []string{"push"}, Repos: []string{"o/r"}}
		mut(&hk)
		return []HookConfig{hk}
	}
	cases := map[string]Config{
		"no providers":        {Hooks: h(func(*HookConfig) {})},
		"no hooks":            {Providers: p},
		"unknown kind":        {Providers: []ProviderConfig{{Name: "f", Kind: "svn"}}, Hooks: h(func(*HookConfig) {})},
		"undeclared provider": {Providers: p, Hooks: h(func(x *HookConfig) { x.Provider = "nope" })},
		"no url, no discover": {Providers: p, Hooks: h(func(x *HookConfig) { x.URL = ""; x.Repos = nil })},
		"no events":           {Providers: p, Hooks: h(func(x *HookConfig) { x.Events = nil })},
		"no repos":            {Providers: p, Hooks: h(func(x *HookConfig) { x.Repos = nil })},
		"malformed repo":      {Providers: p, Hooks: h(func(x *HookConfig) { x.Repos = []string{"demo"} })},
		"duplicate provider":  {Providers: append(p, p...), Hooks: h(func(*HookConfig) {})},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := Config{Providers: p, Hooks: h(func(*HookConfig) {})}
	if err := ok.Validate(); err != nil {
		t.Errorf("a complete config was refused: %v", err)
	}
}

// A repo filter that matches nothing is an error: a typo'd name would otherwise report success
// over zero repos, which reads exactly like a healthy estate.
//
// proven-by: TestSelectHooks_AnEmptySelectionIsAnError
func TestSelectHooks_AnEmptySelectionIsAnError(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{{Name: "f", Kind: "gitea", Server: "https://f"}},
		Hooks: []HookConfig{
			{Provider: "f", URL: "https://l/h", Events: []string{"push"}, Repos: []string{"o/r", "o/s"}},
		},
	}
	o := runOptions{cfg: cfg, onlyRepo: "o/typo"}
	if _, err := selectHooks(cfg, o); err == nil {
		t.Fatal("an empty selection must be an error")
	}

	o = runOptions{cfg: cfg, onlyRepo: "o/S"}
	got, err := selectHooks(cfg, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Repos) != 1 || got[0].Repos[0] != "o/S" {
		t.Fatalf("the repo filter did not narrow to one repo: %+v", got)
	}
}

// A dry run must not touch the forge: the fake fails the test if a write arrives.
func TestPlanAndDryRun_WriteNothing(t *testing.T) {
	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cfg := Config{
		Providers: []ProviderConfig{{Name: "f", Kind: "gitea", Server: srv.URL}},
		Hooks:     []HookConfig{{Provider: "f", URL: "https://l/h", Events: []string{"push"}, Repos: []string{"o/r"}}},
	}
	o := runOptions{cfg: cfg, dryRun: true, hooks: NewHooks, getenv: emptyEnv}
	p, err := plan(context.Background(), o, false)
	if err != nil {
		t.Fatal(err)
	}
	steps := p.steps
	if len(steps) != 1 || steps[0].Action != actionCreate {
		t.Fatalf("plan = %+v", steps)
	}
	if writes != 0 {
		t.Fatalf("%d write(s) during a plan", writes)
	}
	if !changed(steps, false) {
		t.Error("a plan that creates a hook is a change")
	}
}

// The full round trip against a fake forge: create, then present, then removed.
func TestApply_CreatesThenFindsItPresent(t *testing.T) {
	type hook struct {
		ID     int64    `json:"id"`
		Type   string   `json:"type"`
		Active bool     `json:"active"`
		Events []string `json:"events"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
	}
	var store []hook
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(store)
		case http.MethodPost:
			var in hook
			_ = json.NewDecoder(r.Body).Decode(&in)
			in.ID = int64(len(store) + 1)
			store = append(store, in)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(in)
		case http.MethodDelete:
			store = nil
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	cfg := Config{
		Providers: []ProviderConfig{{Name: "f", Kind: "gitea", Server: srv.URL}},
		Hooks: []HookConfig{{Provider: "f", URL: "https://l/h", Events: []string{"push"},
			Repos: []string{"o/r"}}},
	}
	o := runOptions{cfg: cfg, hooks: NewHooks, getenv: emptyEnv}
	ctx := context.Background()

	p, err := plan(ctx, o, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.steps[0].Action != actionCreate {
		t.Fatalf("first plan = %+v", p.steps)
	}
	done := apply(ctx, o, p, false)
	if done[0].Err != "" || len(store) != 1 {
		t.Fatalf("after apply: %+v, store=%d", done, len(store))
	}

	p, err = plan(ctx, o, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.steps[0].Action != actionNone {
		t.Fatalf("second plan = %+v, want none", p.steps)
	}

	p, err = plan(ctx, o, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.steps[0].Action != actionDelete {
		t.Fatalf("delete plan = %+v", p.steps)
	}
	apply(ctx, o, p, true)
	if len(store) != 0 {
		t.Fatalf("store still holds %d hook(s)", len(store))
	}
}

// A forge error on one repo must not stop the others, and must be reported per repo.
func TestPlan_RecordsAForgeErrorPerRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/bad/") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"no admin"}`))
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cfg := Config{
		Providers: []ProviderConfig{{Name: "f", Kind: "gitea", Server: srv.URL}},
		Hooks: []HookConfig{{Provider: "f", URL: "https://l/h", Events: []string{"push"},
			Repos: []string{"bad/r", "good/r"}}},
	}
	o := runOptions{cfg: cfg, hooks: NewHooks, getenv: emptyEnv}
	p, err := plan(context.Background(), o, false)
	if err != nil {
		t.Fatal(err)
	}
	steps := p.steps
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[0].Err == "" {
		t.Error("the forbidden repo was not reported")
	}
	if steps[1].Action != actionCreate {
		t.Errorf("the good repo was not planned: %+v", steps[1])
	}
}

// Active defaults to true: an absent key is not "inactive", and a hook registered inactive
// is the same as no hook with a nicer name.
func TestHookConfig_ActiveDefaultsTrue(t *testing.T) {
	if !(HookConfig{}).ActiveNow() {
		t.Error("an absent active key must mean active")
	}
	no := false
	if (HookConfig{Active: &no}).ActiveNow() {
		t.Error("active: false must stay false")
	}
}

// emptyEnv is the getenv for configs whose references are already literal values.
func emptyEnv(string) string { return "" }

var _ = errors.New
