package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wh.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeHooks is a forge that records calls. It implements Hooks without a network.
type fakeHooks struct {
	repos   []RepoInfo
	hooks   map[string][]Webhook
	listErr error
	created int
	deleted []int64
}

func (f *fakeHooks) ListHooks(_ context.Context, r Repo) ([]Webhook, error) {
	return f.hooks[r.String()], nil
}

func (f *fakeHooks) CreateHook(_ context.Context, r Repo, s Spec) (Webhook, error) {
	f.created++
	f.hooks[r.String()] = append(f.hooks[r.String()], Webhook{ID: int64(100 + f.created), URL: s.URL, Events: s.Events, Active: s.Active})
	return Webhook{ID: int64(100 + f.created), URL: s.URL}, nil
}

func (f *fakeHooks) DeleteHook(_ context.Context, r Repo, id int64) error {
	f.deleted = append(f.deleted, id)
	var kept []Webhook
	for _, h := range f.hooks[r.String()] {
		if h.ID != id {
			kept = append(kept, h)
		}
	}
	f.hooks[r.String()] = kept
	return nil
}

func (f *fakeHooks) ListRepos(_ context.Context, _ string, _ bool) ([]RepoInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}

// The default direction matters: the estate's repos are mostly private, so a discovery whose
// private default were false would find nothing and look like an empty org.
//
// proven-by: TestDiscoverKeep_FiltersArchivedPrivateAndForks
func TestDiscoverKeep_FiltersArchivedPrivateAndForks(t *testing.T) {
	d := Discover{Org: "o"}
	include, exclude, err := d.compiled()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		repo RepoInfo
		want bool
	}{
		"plain":            {RepoInfo{Repo: Repo{Owner: "o", Name: "live"}}, true},
		"private kept":     {RepoInfo{Repo: Repo{Owner: "o", Name: "priv"}, Private: true}, true},
		"archived dropped": {RepoInfo{Repo: Repo{Owner: "o", Name: "old"}, Archived: true}, false},
		"fork dropped":     {RepoInfo{Repo: Repo{Owner: "o", Name: "f"}, Fork: true}, false},
	}
	for name, tc := range cases {
		if got := d.keep(tc.repo, include, exclude); got != tc.want {
			t.Errorf("%s: keep = %v, want %v", name, got, tc.want)
		}
	}

	no := false
	d.IncludePrivate = &no
	if d.keep(RepoInfo{Repo: Repo{Owner: "o", Name: "priv"}, Private: true}, include, exclude) {
		t.Error("include_private: false did not drop a private repo")
	}
}

func TestDiscoverKeep_IncludeAndExclude(t *testing.T) {
	d := Discover{Org: "o", Include: `^lh-`, Exclude: `-lab$`}
	include, exclude, err := d.compiled()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"lh-test":      true,
		"lh-private":   true,
		"lh-lab":       false,
		"driver-lab":   false,
		"other-lh-one": false,
	} {
		if got := d.keep(RepoInfo{Repo: Repo{Owner: "o", Name: name}}, include, exclude); got != want {
			t.Errorf("%s: keep = %v, want %v", name, got, want)
		}
	}
}

func TestDiscover_RefusesABadPattern(t *testing.T) {
	if _, _, err := (Discover{Org: "o", Include: "["}).compiled(); err == nil {
		t.Fatal("an unparseable include was accepted")
	}
}

// proven-by: TestDiscoverRepos_AppliesTheFilterAndSorts
func TestDiscoverRepos_AppliesTheFilterAndSorts(t *testing.T) {
	d := Discover{Org: "it-test", Include: `^lh-`}
	list := func(owner string, isOrg bool) ([]RepoInfo, error) {
		if owner != "it-test" || !isOrg {
			t.Errorf("listed owner=%q isOrg=%v", owner, isOrg)
		}
		return []RepoInfo{
			{Repo: Repo{Owner: "it-test", Name: "lh-test"}},
			{Repo: Repo{Owner: "it-test", Name: "driver-lab"}},
			{Repo: Repo{Owner: "it-test", Name: "lh-private"}},
			{Repo: Repo{Owner: "it-test", Name: "archived"}, Archived: true},
		}, nil
	}
	got, err := discoverRepos(d, list)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "lh-private" || got[1].Name != "lh-test" {
		t.Fatalf("discoverRepos = %v, want the two lh- repos, sorted", got)
	}
}

func TestDiscover_RequiresExactlyOneOfOrgAndUser(t *testing.T) {
	if _, err := (Discover{}).owner(); err == nil {
		t.Error("neither org nor user was accepted")
	}
	if _, err := (Discover{Org: "a", User: "b"}).owner(); err == nil {
		t.Error("both org and user were accepted")
	}
	if o, err := (Discover{User: "me"}).owner(); err != nil || o != "me" {
		t.Errorf("user-only = %q, %v", o, err)
	}
}

// proven-by: TestMergeRepos_UnionWithoutDuplicates
func TestMergeRepos_UnionWithoutDuplicates(t *testing.T) {
	got := mergeRepos(
		[]Repo{{Owner: "it-test", Name: "lh-test"}},
		[]Repo{{Owner: "it-test", Name: "lh-test"}, {Owner: "it-test", Name: "lh-private"}},
	)
	if len(got) != 2 {
		t.Fatalf("mergeRepos = %v, want the union", got)
	}
	if got[0].String() != "it-test/lh-private" || got[1].String() != "it-test/lh-test" {
		t.Errorf("mergeRepos = %v, want sorted", got)
	}
}

// A hook with no url is legitimate (the cluster supplies it); one with no repos and no discover
// block is not, because it names nothing to act on.
func TestValidate_AcceptsAClusterURLWithDiscover(t *testing.T) {
	c := Config{
		Providers: []ProviderConfig{{Name: "f", Kind: "gitea", Server: "https://f"}},
		Hooks: []HookConfig{{
			Provider: "f", Events: []string{"push"},
			Discover: &Discover{Org: "it-test"},
		}},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("a cluster-url hook with discover was refused: %v", err)
	}
}

// The URL comes from the flag, then the config, then the cluster — and the error when none of
// them supplies one names all three.
//
// proven-by: TestHookURL_FlagThenConfigThenCluster
func TestHookURL_FlagThenConfigThenCluster(t *testing.T) {
	h := HookConfig{Provider: "f", URL: "https://from-config/hook"}
	prov := Provider{Name: "f", Cluster: ClusterSources{HookURL: "https://from-cluster/hook"}}

	if got, _ := (runOptions{onlyURL: "https://flag/hook"}).hookURL(h, prov); got != "https://flag/hook" {
		t.Errorf("flag did not win: %q", got)
	}
	if got, _ := (runOptions{}).hookURL(h, prov); got != "https://from-config/hook" {
		t.Errorf("config did not beat the cluster: %q", got)
	}
	h.URL = ""
	if got, _ := (runOptions{}).hookURL(h, prov); got != "https://from-cluster/hook" {
		t.Errorf("cluster was not used: %q", got)
	}
	if _, err := (runOptions{}).hookURL(h, Provider{Name: "f"}); err == nil {
		t.Error("no url anywhere was accepted")
	}
}

// prunePlan is the reconcile half: in-scope repos not in the wanted set lose OUR hook, and a
// hook on another URL on the same repo is left alone.
//
// proven-by: TestPrunePlan_OnlyRemovesOursOutsideTheWantedSet
func TestPrunePlan_OnlyRemovesOursOutsideTheWantedSet(t *testing.T) {
	const ours = "https://ours/hook"
	f := &fakeHooks{
		repos: []RepoInfo{
			{Repo: Repo{Owner: "it-test", Name: "wanted"}},
			{Repo: Repo{Owner: "it-test", Name: "stale"}},
			{Repo: Repo{Owner: "it-test", Name: "someone-elses"}},
		},
		hooks: map[string][]Webhook{
			"it-test/wanted":        {{ID: 1, URL: ours}},
			"it-test/stale":         {{ID: 2, URL: ours}},
			"it-test/someone-elses": {{ID: 3, URL: "https://elsewhere/hook"}},
		},
	}
	h := HookConfig{Provider: "f", Discover: &Discover{Org: "it-test"}}
	want := []Repo{{Owner: "it-test", Name: "wanted"}}

	steps, err := prunePlan(context.Background(), runOptions{}, Provider{Name: "f"}, f, h, ours, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("prune steps = %+v, want exactly the stale one", steps)
	}
	if steps[0].Repo != "it-test/stale" || steps[0].Action != actionDelete || !steps[0].Prune {
		t.Errorf("pruned the wrong thing: %+v", steps[0])
	}
}

// A forge that cannot list repos makes -prune an error, never a silent "nothing to prune":
// an empty scope and a clean estate would read identically.
func TestPrunePlan_ReportsAListFailure(t *testing.T) {
	f := &fakeHooks{listErr: errors.New("403 forbidden")}
	h := HookConfig{Provider: "f", Discover: &Discover{Org: "it-test"}}
	if _, err := prunePlan(context.Background(), runOptions{}, Provider{Name: "f"}, f, h, "https://ours/hook", nil); err == nil {
		t.Fatal("a list failure was swallowed")
	}
}

// An explicit repo list is the wanted set, and the discover query is then only the SCOPE. The
// alternative — merging them — makes the list a no-op as soon as discover matches the same org,
// and leaves no way to express "these repos, and nothing else in this org".
//
// proven-by: TestDesiredRepos_ExplicitListWinsOverDiscover
func TestDesiredRepos_ExplicitListWinsOverDiscover(t *testing.T) {
	f := &fakeHooks{repos: []RepoInfo{
		{Repo: Repo{Owner: "it-test", Name: "lh-test"}},
		{Repo: Repo{Owner: "it-test", Name: "lh-private"}},
	}}
	h := HookConfig{
		Provider: "it-test/lh-test",
		Repos:    []string{"it-test/lh-test"},
		Discover: &Discover{Org: "it-test"},
	}
	got, err := desiredRepos(context.Background(), h, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].String() != "it-test/lh-test" {
		t.Fatalf("desiredRepos = %v, want only the named repo", got)
	}

	// ...and with no explicit list, discover IS the wanted set.
	h.Repos = nil
	got, err = desiredRepos(context.Background(), h, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("desiredRepos = %v, want both discovered repos", got)
	}
}

// The endpoint belongs to whichever Lighthouse receives that forge's events, so it is read per
// provider — `hook` in jx for GitHub, `forge-webhooks` in forgejo for Forgejo. A single global
// read would give every provider the same host, which is the bug this split prevents.
//
// proven-by: TestResolve_ReadsTheClusterPerProvider
func TestResolve_ReadsTheClusterPerProvider(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "github", Kind: "github", Route: "hook"},
			{Name: "forgejo", Kind: "gitea", Server: "https://f", Route: "forgejo/forge-webhooks"},
		},
		Hooks: []HookConfig{
			{Provider: "github", Events: []string{"push"}, Repos: []string{"o/r"}},
			{Provider: "forgejo", Events: []string{"push"}, Repos: []string{"o/r"}},
		},
	}
	read := func(pc ProviderConfig) ClusterSources {
		return ClusterSources{HookURL: "https://" + pc.Route + "/hook", Route: "httproute " + pc.Route}
	}
	r, err := resolve(cfg, cfg.Hooks, emptyEnv, read)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.providers["github"].Cluster.HookURL; got != "https://hook/hook" {
		t.Errorf("github endpoint = %q", got)
	}
	if got := r.providers["forgejo"].Cluster.HookURL; got != "https://forgejo/forge-webhooks/hook" {
		t.Errorf("forgejo endpoint = %q, want its own route's", got)
	}
}

// The route reference is "name" or "namespace/name", and the namespace defaults, because most
// providers name a route in the dev namespace and writing it twice is how a copy goes wrong.
func TestParseRoute_DefaultsTheNamespace(t *testing.T) {
	if got := parseRoute("hook", "jx"); got.Namespace != "jx" || got.Name != "hook" {
		t.Errorf("parseRoute = %+v", got)
	}
	if got := parseRoute("forgejo/forge-webhooks", "jx"); got.Namespace != "forgejo" || got.Name != "forge-webhooks" {
		t.Errorf("parseRoute = %+v", got)
	}
}
