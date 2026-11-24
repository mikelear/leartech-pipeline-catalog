package main

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadConfig reads and structurally validates only. Which credentials a run needs depends on
// which hooks it selects, so a config carrying ${GITHUB_TOKEN} loads fine on a machine that
// has no GitHub token and is running against Forgejo.
//
// proven-by: TestLoadConfig_DoesNotResolveReferences
func TestLoadConfig_DoesNotResolveReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wh.yaml")
	if err := os.WriteFile(path, []byte(`
providers:
  - name: forgejo
    kind: gitea
    server: https://forgejo.example
    token: ${FORGE_TOKEN}
  - name: github
    kind: github
    token: ${GITHUB_TOKEN}
hooks:
  - provider: forgejo
    url: https://hook.example/hook
    secret: ${HMAC}
    events: [push]
    repos: [it-test/lh-test]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a config whose unselected provider has an unset reference was refused: %v", err)
	}
	if c.Providers[0].Token != "${FORGE_TOKEN}" {
		t.Errorf("LoadConfig resolved a reference (%q); only resolve may", c.Providers[0].Token)
	}
}

// Resolution is limited to the providers the selected hooks name, which is what makes
// `-provider forgejo` usable without a GitHub token.
//
// proven-by: TestResolve_ExpandsOnlyWhatTheRunUses
func TestResolve_ExpandsOnlyWhatTheRunUses(t *testing.T) {
	c := Config{
		Providers: []ProviderConfig{
			{Name: "forgejo", Kind: "gitea", Server: "https://f", Token: "${FORGE_TOKEN}"},
			{Name: "github", Kind: "github", Token: "${GITHUB_TOKEN}"},
		},
		Hooks: []HookConfig{
			{Provider: "forgejo", URL: "https://h/1", Secret: "${HMAC}", Events: []string{"push"}, Repos: []string{"o/r"}},
			{Provider: "github", URL: "https://h/2", Secret: "${HMAC}", Events: []string{"push"}, Repos: []string{"o/r"}},
		},
	}
	onlyForgejo := []HookConfig{c.Hooks[0]}
	env := func(k string) string {
		return map[string]string{"FORGE_TOKEN": "ft", "HMAC": "s"}[k]
	}
	r, err := resolve(c, onlyForgejo, env, nil)
	if err != nil {
		t.Fatalf("a run selecting only forgejo failed on the GitHub token: %v", err)
	}
	if len(r.providers) != 1 || r.providers["forgejo"].Token != "ft" {
		t.Fatalf("resolved %+v, want forgejo only", r.providers)
	}
	if r.hooks[0].Secret != "s" {
		t.Errorf("the hook secret was not expanded: %q", r.hooks[0].Secret)
	}

	// The control: selecting both needs both, and the missing one is named.
	if _, err := resolve(c, c.Hooks, env, nil); err == nil {
		t.Fatal("selecting the github hook without GITHUB_TOKEN was accepted")
	}
}
