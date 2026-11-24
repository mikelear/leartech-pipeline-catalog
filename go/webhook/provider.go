// Package main is the provider seam: one hook operation, two REST shapes.
//
// The two forges differ in exactly the fields a webhook is made of, so the difference lives
// here rather than in the caller:
//
//	field          GitHub                    Gitea/Forgejo
//	hook type      name: "web"               type: "gitea"   (Forgejo keeps Gitea's names)
//	auth           Authorization: Bearer t    Authorization: token t
//	path prefix    /repos/...                 /api/v1/repos/...
//
// A driver that sent GitHub's shape to Forgejo gets a 422 the caller reads as "already
// exists", so the wrong field fails SILENTLY. That is why each driver is tested against a
// fake server that asserts which field it sends and which it omits.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Kind is the forge family a provider speaks.
type Kind string

const (
	// KindGitHub is GitHub's REST shape.
	KindGitHub Kind = "github"
	// KindGitea is Gitea's, which Forgejo also speaks.
	KindGitea Kind = "gitea"
)

// Repo is an owner/name pair, split so a driver does not parse a path.
type Repo struct {
	Owner string
	Name  string
}

// String renders the repo the way a user writes it.
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// ParseRepo splits "owner/name" and refuses anything else, because a repo filter that
// silently matches nothing is indistinguishable from a forge with no hooks.
// proven-by: TestParseRepo_RefusesAMalformedName
func ParseRepo(s string) (Repo, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return Repo{}, fmt.Errorf("repo %q is not owner/name", s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// Webhook is one hook as the forge reports it.
type Webhook struct {
	ID     int64    `json:"id"`
	URL    string   `json:"url"`
	Kind   string   `json:"kind"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
}

// Spec is what a caller wants registered. The secret is a plain parameter: this package
// reads no environment, so who may see a credential stays the caller's decision.
type Spec struct {
	URL    string
	Secret string
	Events []string
	Active bool
}

// Provider is one configured forge.
type Provider struct {
	Name  string
	Kind  Kind
	Base  string
	Token string
}

// Hooks is the hook surface a provider implements.
type Hooks interface {
	ListHooks(ctx context.Context, repo Repo) ([]Webhook, error)
	CreateHook(ctx context.Context, repo Repo, spec Spec) (Webhook, error)
	DeleteHook(ctx context.Context, repo Repo, id int64) error
	ListRepos(ctx context.Context, owner string, isOrg bool) ([]RepoInfo, error)
}

// NewHooks returns the driver for p, or an error naming the kind.
// proven-by: TestNewHooks_RefusesAnUnknownKind
func NewHooks(p Provider) (Hooks, error) {
	switch p.Kind {
	case KindGitHub:
		base := strings.TrimSuffix(p.Base, "/")
		if base == "" {
			base = "https://api.github.com"
		}
		return &gitHubHooks{base: base, token: p.Token, http: http.DefaultClient}, nil
	case KindGitea:
		base := strings.TrimSuffix(p.Base, "/")
		if base == "" {
			return nil, fmt.Errorf("provider %q: a gitea/forgejo provider needs a server", p.Name)
		}
		return &giteaHooks{base: base, token: p.Token, http: http.DefaultClient}, nil
	default:
		return nil, fmt.Errorf("provider %q: unknown kind %q (want github or gitea)", p.Name, p.Kind)
	}
}

// do performs one request and maps a non-2xx answer to an error that names the status.
func do(ctx context.Context, hc *http.Client, method, url, authHeader string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s -> %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// gitHubHooks is GitHub's repo-hook surface.
type gitHubHooks struct {
	base  string
	token string
	http  *http.Client
}

type ghHook struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

func (h ghHook) toWebhook() Webhook {
	return Webhook{ID: h.ID, URL: h.Config.URL, Kind: h.Name, Active: h.Active, Events: h.Events}
}

func (g *gitHubHooks) auth() string {
	if g.token == "" {
		return ""
	}
	return "Bearer " + g.token
}

func (g *gitHubHooks) ListHooks(ctx context.Context, repo Repo) ([]Webhook, error) {
	var got []ghHook
	url := fmt.Sprintf("%s/repos/%s/%s/hooks", g.base, repo.Owner, repo.Name)
	if err := do(ctx, g.http, http.MethodGet, url, g.auth(), nil, &got); err != nil {
		return nil, err
	}
	out := make([]Webhook, 0, len(got))
	for _, h := range got {
		out = append(out, h.toWebhook())
	}
	return out, nil
}

func (g *gitHubHooks) CreateHook(ctx context.Context, repo Repo, spec Spec) (Webhook, error) {
	config := map[string]any{"url": spec.URL, "content_type": "json"}
	if spec.Secret != "" {
		config["secret"] = spec.Secret
	}
	payload := map[string]any{
		"name":   "web",
		"active": spec.Active,
		"events": spec.Events,
		"config": config,
	}
	var created ghHook
	url := fmt.Sprintf("%s/repos/%s/%s/hooks", g.base, repo.Owner, repo.Name)
	if err := do(ctx, g.http, http.MethodPost, url, g.auth(), payload, &created); err != nil {
		return Webhook{}, err
	}
	return created.toWebhook(), nil
}

func (g *gitHubHooks) DeleteHook(ctx context.Context, repo Repo, id int64) error {
	url := fmt.Sprintf("%s/repos/%s/%s/hooks/%d", g.base, repo.Owner, repo.Name, id)
	return do(ctx, g.http, http.MethodDelete, url, g.auth(), nil, nil)
}

// giteaHooks is Gitea/Forgejo's repo-hook surface.
type giteaHooks struct {
	base  string
	token string
	http  *http.Client
}

type gtHook struct {
	ID     int64    `json:"id"`
	Type   string   `json:"type"`
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

func (h gtHook) toWebhook() Webhook {
	return Webhook{ID: h.ID, URL: h.Config.URL, Kind: h.Type, Active: h.Active, Events: h.Events}
}

// giteaHookType is the hook type both forges accept. Forgejo keeps Gitea's hook-type names
// for compatibility and answers "gitea"; sending "forgejo" is rejected by older instances.
const giteaHookType = "gitea"

func (g *giteaHooks) auth() string {
	if g.token == "" {
		return ""
	}
	return "token " + g.token
}

func (g *giteaHooks) ListHooks(ctx context.Context, repo Repo) ([]Webhook, error) {
	var got []gtHook
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/hooks", g.base, repo.Owner, repo.Name)
	if err := do(ctx, g.http, http.MethodGet, url, g.auth(), nil, &got); err != nil {
		return nil, err
	}
	out := make([]Webhook, 0, len(got))
	for _, h := range got {
		out = append(out, h.toWebhook())
	}
	return out, nil
}

func (g *giteaHooks) CreateHook(ctx context.Context, repo Repo, spec Spec) (Webhook, error) {
	config := map[string]any{"url": spec.URL, "content_type": "json"}
	if spec.Secret != "" {
		config["secret"] = spec.Secret
	}
	payload := map[string]any{
		"type":   giteaHookType,
		"active": spec.Active,
		"events": spec.Events,
		"config": config,
	}
	var created gtHook
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/hooks", g.base, repo.Owner, repo.Name)
	if err := do(ctx, g.http, http.MethodPost, url, g.auth(), payload, &created); err != nil {
		return Webhook{}, err
	}
	return created.toWebhook(), nil
}

func (g *giteaHooks) DeleteHook(ctx context.Context, repo Repo, id int64) error {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/hooks/%d", g.base, repo.Owner, repo.Name, id)
	return do(ctx, g.http, http.MethodDelete, url, g.auth(), nil, nil)
}

// ListRepos returns the repos an org or a user owns, for a discover block.
//
// Both forges page this endpoint and neither defaults to a page size that covers a real org,
// so it pages until a short page comes back. A repo list that silently stops at 30 is how a
// discovery-based run looks complete and leaves repos unhooked.
func (g *giteaHooks) ListRepos(ctx context.Context, owner string, isOrg bool) ([]RepoInfo, error) {
	kind := "users"
	if isOrg {
		kind = "orgs"
	}
	var out []RepoInfo
	for page := 1; page <= 100; page++ {
		var batch []struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Archived bool   `json:"archived"`
			Private  bool   `json:"private"`
			Fork     bool   `json:"fork"`
		}
		url := fmt.Sprintf("%s/api/v1/%s/%s/repos?limit=50&page=%d", g.base, kind, owner, page)
		if err := do(ctx, g.http, http.MethodGet, url, g.auth(), nil, &batch); err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, RepoInfo{
				Repo:     Repo{Owner: owner, Name: b.Name},
				Archived: b.Archived, Private: b.Private, Fork: b.Fork,
			})
		}
		if len(batch) < 50 {
			return out, nil
		}
	}
	return out, nil
}

// ListRepos is the GitHub equivalent. /users/{u}/repos lists what a USER owns rather than what
// they can see, which is the distinction that matters: a token with org-wide access would
// otherwise pull in every repo it can read.
func (g *gitHubHooks) ListRepos(ctx context.Context, owner string, isOrg bool) ([]RepoInfo, error) {
	kind := "users"
	if isOrg {
		kind = "orgs"
	}
	var out []RepoInfo
	for page := 1; page <= 100; page++ {
		var batch []struct {
			Name     string `json:"name"`
			Archived bool   `json:"archived"`
			Private  bool   `json:"private"`
			Fork     bool   `json:"fork"`
		}
		url := fmt.Sprintf("%s/%s/%s/repos?per_page=100&page=%d", g.base, kind, owner, page)
		if err := do(ctx, g.http, http.MethodGet, url, g.auth(), nil, &batch); err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, RepoInfo{
				Repo:     Repo{Owner: owner, Name: b.Name},
				Archived: b.Archived, Private: b.Private, Fork: b.Fork,
			})
		}
		if len(batch) < 100 {
			return out, nil
		}
	}
	return out, nil
}
