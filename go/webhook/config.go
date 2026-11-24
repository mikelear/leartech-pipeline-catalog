package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the whole input: which forges, and which hooks on which repos.
type Config struct {
	Providers []ProviderConfig `yaml:"providers"`
	Hooks     []HookConfig     `yaml:"hooks"`
}

// ProviderConfig names one forge and how to reach it.
type ProviderConfig struct {
	Name   string `yaml:"name"`
	Kind   string `yaml:"kind"`
	Server string `yaml:"server"`
	// Token is a REFERENCE such as ${FORGE_TOKEN}, expanded only for a provider the run uses.
	Token string `yaml:"token"`
	// Route names the Gateway API HTTPRoute (or, on a cluster that still has one, the Ingress)
	// whose hostname is this provider's hook endpoint: "name" or "namespace/name". The endpoint
	// belongs to whichever Lighthouse receives THAT forge's events, and this estate runs two —
	// `hook` in `jx` for GitHub, `forge-webhooks` in `forgejo` for Forgejo.
	Route string `yaml:"route"`
}

// HookConfig is one hook target: a URL, its secret and event set, and the repos it goes on.
type HookConfig struct {
	Provider string `yaml:"provider"`
	URL      string `yaml:"url"`
	// Secret is a REFERENCE too. Empty means the hook is registered unsigned, and that is
	// reported rather than assumed, because an unsigned hook looks correct and Lighthouse
	// then rejects every delivery from it.
	Secret string   `yaml:"secret"`
	Events []string `yaml:"events"`
	Active *bool    `yaml:"active"`
	// Repos is the explicit half of the target set. Empty is allowed when Discover is set.
	Repos []string `yaml:"repos"`
	// Discover is the query half, and the only thing that gives -prune a scope.
	Discover *Discover `yaml:"discover"`
}

// reference matches ${NAME}, the only expansion this file allows: there is no other
// substitution, so a config file is data.
var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expand resolves every ${NAME} in s from the environment.
//
// An unset variable is an ERROR naming it, not an empty string: an empty credential is a 401
// that reads like a forge problem, and an empty SECRET registers an unsigned hook that the
// forge accepts and Lighthouse then rejects every delivery from.
// proven-by: TestExpand_AnUnsetVariableIsAnError
func expand(s string, getenv func(string) string) (string, error) {
	var missing []string
	out := reference.ReplaceAllStringFunc(s, func(m string) string {
		name := reference.FindStringSubmatch(m)[1]
		if v := getenv(name); v != "" {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%s: variable(s) %s not set", s, strings.Join(missing, ", "))
	}
	return out, nil
}

// LoadConfig reads and structurally validates a config file. It resolves NO references: which
// credentials a run needs depends on which hooks it selects, so expansion happens in resolve.
// proven-by: TestLoadConfig_DoesNotResolveReferences
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate refuses a config that could only half-work: a hook referring to a provider that
// does not exist, a provider with an unknown kind, a hook with no repos, a malformed repo.
// proven-by: TestValidate_RefusesAHalfWorkingConfig
func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New("no providers: a config with no forge can do nothing")
	}
	seen := make(map[string]bool, len(c.Providers))
	for _, p := range c.Providers {
		if p.Name == "" {
			return errors.New("a provider has no name")
		}
		if seen[p.Name] {
			return fmt.Errorf("provider %q is declared twice", p.Name)
		}
		seen[p.Name] = true
		if p.Kind != string(KindGitHub) && p.Kind != string(KindGitea) {
			return fmt.Errorf("provider %q: kind %q is not github or gitea", p.Name, p.Kind)
		}
	}
	if len(c.Hooks) == 0 {
		return errors.New("no hooks: nothing to do")
	}
	for i, h := range c.Hooks {
		// A hook with no url takes the running cluster's, which is the point of reading it
		// from the cluster at all; what is refused is having neither.
		_ = i
		if !seen[h.Provider] {
			return fmt.Errorf("hook %s refers to provider %q, which is not declared", h.name(), h.Provider)
		}
		if len(h.Events) == 0 {
			return fmt.Errorf("hook %s has no events", h.name())
		}
		if len(h.Repos) == 0 && h.Discover == nil {
			return fmt.Errorf("hook %s has neither repos nor a discover block", h.name())
		}
		if h.Discover != nil {
			if _, err := h.Discover.owner(); err != nil {
				return fmt.Errorf("hook %s: %w", h.name(), err)
			}
			if _, _, err := h.Discover.compiled(); err != nil {
				return fmt.Errorf("hook %s: %w", h.name(), err)
			}
		}
		for _, r := range h.Repos {
			if _, err := ParseRepo(r); err != nil {
				return fmt.Errorf("hook %s: %w", h.name(), err)
			}
		}
		kind := Kind(c.Providers[kindOf(c.Providers, h.Provider)].Kind)
		if err := checkEvents(kind, h.Events); err != nil {
			return fmt.Errorf("hook %s (%s provider %q): %w", h.URL, kind, h.Provider, err)
		}
	}
	return nil
}

// name is what an error calls this hook before its URL is known: a hook that takes the
// cluster's URL has no url in the file, and a message quoting an empty string names nothing.
func (h HookConfig) name() string {
	if h.URL != "" {
		return h.URL
	}
	return "(" + h.Provider + " hook with the cluster's url)"
}

// kindOf returns the index of name's declaration, which Validate has already established.
func kindOf(ps []ProviderConfig, name string) int {
	for i, p := range ps {
		if p.Name == name {
			return i
		}
	}
	return 0
}

// ActiveNow reports whether the hook should be registered active. An absent key means active,
// which is what the incumbent did and what every other default in this estate is: a hook
// registered inactive is the same as no hook with a nicer name.
func (h HookConfig) ActiveNow() bool {
	if h.Active == nil {
		return true
	}
	return *h.Active
}

// Provider returns the declaration for name.
func (c Config) Provider(name string) (ProviderConfig, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return ProviderConfig{}, false
}

// resolved is the config narrowed to one run: the selected hooks, with every reference their
// providers need expanded. A run filtered to one provider therefore needs only THAT provider's
// token — which is what makes `-provider forgejo` usable on a machine with no GitHub token.
type resolved struct {
	providers map[string]Provider
	hooks     []HookConfig
}

// resolve expands the references the run uses; an unset one is an error.
// proven-by: TestResolve_ExpandsOnlyWhatTheRunUses
// proven-by: TestResolve_ExpandsOnlyWhatTheRunUses
func resolve(c Config, chosen []HookConfig, getenv func(string) string, read func(ProviderConfig) ClusterSources) (resolved, error) {
	out := resolved{providers: make(map[string]Provider, len(c.Providers)), hooks: make([]HookConfig, 0, len(chosen))}
	for _, h := range chosen {
		pc, ok := c.Provider(h.Provider)
		if !ok {
			return resolved{}, fmt.Errorf("hook %s: provider %q is not declared", h.URL, h.Provider)
		}
		if _, done := out.providers[pc.Name]; !done {
			token, err := expand(pc.Token, getenv)
			if err != nil {
				return resolved{}, fmt.Errorf("provider %q token: %w", pc.Name, err)
			}
			prov := Provider{
				Name: pc.Name, Kind: Kind(pc.Kind), Base: pc.Server, Token: token,
			}
			if read != nil {
				prov.Cluster = read(pc)
			}
			out.providers[pc.Name] = prov
		}
		secret, err := expand(h.Secret, getenv)
		if err != nil {
			return resolved{}, fmt.Errorf("hook %s secret: %w", h.URL, err)
		}
		hc := h
		hc.Secret = secret
		out.hooks = append(out.hooks, hc)
	}
	return out, nil
}
