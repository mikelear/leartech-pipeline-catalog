// webhook — register, list and remove forge webhooks from a config file, for GitHub or Forgejo.
//
// It is the port of `jx gitops webhook update`, which reads SourceRepository CRs from a JX
// cluster. That cluster does not exist in the new world, so the source of truth is a file:
// which forge, which repos, which hook URL, which events, and WHICH SECRET BY NAME.
//
// Every value is a flag as well as a config key, and the flag wins, so pointing a run at one
// throwaway repo on a different cluster is one command rather than an edited file:
//
//	webhook update -config webhooks.yaml -repo it-test/lh-test -dry-run
//
// Neither the token nor the hook secret comes from a flag's value: both are ${NAME} references
// in the config, expanded from the environment, so neither appears in a pod spec, a shell
// history or a log line.
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

// action is what a run does to one repo for one hook.
type action string

const (
	// actionCreate means the repo has no hook on this URL.
	actionCreate action = "create"
	// actionReplace means a hook on this URL exists with a different event set or state.
	actionReplace action = "replace"
	// actionNone means the hook is already exactly as configured.
	actionNone action = "none"
	// actionDelete means a delete run removes the hook on this URL.
	actionDelete action = "delete"
)

// step is one planned or performed change: which repo, which hook, what happened.
type step struct {
	Provider string `json:"provider"`
	Repo     string `json:"repo"`
	URL      string `json:"url"`
	Action   action `json:"action"`
	HookID   int64  `json:"hook_id,omitempty"`
	// Other is every other hook URL already on the repo. Reported rather than ignored: a repo
	// that carries a hook on a DIFFERENT endpoint and is about to gain this one is what a
	// mistyped -url, or a stale endpoint, looks like.
	Other []string `json:"other_hooks,omitempty"`
	// Prune marks a step that removes a hook the config no longer asks for, so a summary can
	// say how many of the deletes were reconciliation rather than a requested removal.
	Prune bool   `json:"prune,omitempty"`
	Err   string `json:"error,omitempty"`
}

// sameEvents reports whether two event sets are equal, order aside.
func sameEvents(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, e := range a {
		set[e] = struct{}{}
	}
	for _, e := range b {
		if _, ok := set[e]; !ok {
			return false
		}
	}
	return true
}

// sameURL compares two hook endpoints, ignoring a trailing slash and host case. A config
// edited by hand has one and a forge's stored value does not, and treating those as different
// doubles the hook.
// proven-by: TestSameURL_IgnoresATrailingSlash
func sameURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

// decide is the whole idempotency rule, in one function, so it is testable without a forge.
//
// Matching is by URL and not by the forge's duplicate error: both forges answer 422, which is
// indistinguishable from a rejected payload without listing first.
// proven-by: TestDecide_CoversCreateReplaceAndNone
func decide(have []Webhook, spec Spec, want bool) (action, int64) {
	for _, h := range have {
		if !sameURL(h.URL, spec.URL) {
			continue
		}
		if !want {
			return actionDelete, h.ID
		}
		if sameEvents(h.Events, spec.Events) && h.Active == spec.Active {
			return actionNone, h.ID
		}
		// Neither forge offers a hook patch covering both fields, so a hook that moved is
		// removed and created again.
		return actionReplace, h.ID
	}
	if want {
		return actionCreate, 0
	}
	return actionNone, 0
}

// runOptions is everything a run needs, after flags and config are merged.
type runOptions struct {
	cfg        Config
	onlyProv   string
	onlyRepo   string
	onlyURL    string
	envSecret  string
	dryRun     bool
	warnOnFail bool
	asJSON     bool
	getenv     func(string) string
	hooks      func(Provider) (Hooks, error)
	// cluster is what the running cluster supplied: the hook endpoint, and the HMAC secret.
	cluster ClusterSources
	// prune reconciles the forge to the config inside the DISCOVERED scope. It is opt-in:
	// create/replace is safe on its own, a delete never is.
	prune bool
}

// selectHooks filters the config's hooks by provider, url and repo, and narrows each to the
// one repo a -repo filter names.
//
// A filter that matches nothing is an ERROR: a typo'd repo name would otherwise report success
// over zero repos, which reads exactly like a healthy estate.
// proven-by: TestSelectHooks_AnEmptySelectionIsAnError
func selectHooks(c Config, o runOptions) ([]HookConfig, error) {
	var out []HookConfig
	for _, h := range c.Hooks {
		if o.onlyProv != "" && h.Provider != o.onlyProv {
			continue
		}
		if o.onlyURL != "" && !sameURL(h.URL, o.onlyURL) {
			continue
		}
		copied := h
		if o.onlyRepo != "" {
			if !repoListed(h.Repos, o.onlyRepo) {
				continue
			}
			copied.Repos = []string{o.onlyRepo}
		}
		out = append(out, copied)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no hook in the config matches provider=%q repo=%q url=%q",
			o.onlyProv, o.onlyRepo, o.onlyURL)
	}
	return out, nil
}

// repoListed reports whether repo is in reps, compared case-insensitively.
func repoListed(reps []string, repo string) bool {
	for _, r := range reps {
		if strings.EqualFold(r, repo) {
			return true
		}
	}
	return false
}

// hookKey identifies one hook within one provider.
func hookKey(provider, url string) string {
	return provider + "\x00" + strings.TrimRight(url, "/")
}

// otherURLs is every hook URL on the repo that is not this one.
func otherURLs(have []Webhook, url string) []string {
	var out []string
	for _, h := range have {
		if h.URL != "" && !sameURL(h.URL, url) {
			out = append(out, h.URL)
		}
	}
	sort.Strings(out)
	return out
}

// aplan is a computed run: the steps, and the resolved clients and specs needed to carry them
// out. Keeping them together is what lets `apply` not re-read the environment — a second read
// could resolve a different secret than the plan was checked against.
type aplan struct {
	steps []step
	specs map[string]Spec
	provs map[string]Provider
}

// plan computes every step for the run. It calls only ListHooks, so a plan changes nothing on
// the forge.
func plan(ctx context.Context, o runOptions, deleteOnly bool) (aplan, error) {
	chosen, err := selectHooks(o.cfg, o)
	if err != nil {
		return aplan{}, err
	}
	r, err := resolve(o.cfg, chosen, o.getenv)
	if err != nil {
		return aplan{}, err
	}
	p := aplan{specs: make(map[string]Spec, len(r.hooks)), provs: r.providers}
	clients := make(map[string]Hooks, len(r.providers))
	for i := range r.hooks {
		h := &r.hooks[i]
		prov := r.providers[h.Provider]
		hc, ok := clients[prov.Name]
		if !ok {
			hc, err = o.hooks(prov)
			if err != nil {
				return aplan{}, err
			}
			clients[prov.Name] = hc
		}
		// The URL and the secret are resolved here, once, for the same reason: what a run
		// acts on must be what the plan was checked against.
		url, uerr := o.hookURL(*h)
		if uerr != nil {
			return aplan{}, uerr
		}
		spec := Spec{URL: url, Secret: h.Secret, Events: h.Events, Active: h.ActiveNow()}
		if o.envSecret != "" {
			spec.Secret = o.envSecret
		}
		if spec.Secret == "" {
			spec.Secret = o.cluster.HMAC
		}
		p.specs[hookKey(prov.Name, url)] = spec

		want, werr := desiredRepos(ctx, *h, hc)
		if werr != nil {
			return aplan{}, werr
		}
		for _, repo := range want {
			have, lerr := hc.ListHooks(ctx, repo)
			if lerr != nil {
				p.steps = append(p.steps, step{Provider: prov.Name, Repo: repo.String(), URL: url,
					Action: actionNone, Err: lerr.Error()})
				continue
			}
			act, id := decide(have, spec, !deleteOnly)
			p.steps = append(p.steps, step{Provider: prov.Name, Repo: repo.String(), URL: url,
				Action: act, HookID: id, Other: otherURLs(have, url)})
		}
		if !o.prune {
			continue
		}
		if h.Discover == nil {
			return aplan{}, fmt.Errorf("hook %s: -prune needs a discover block — with only a repo "+
				"list there is no scope to reconcile against", h.name())
		}
		pruneSteps, perr := prunePlan(ctx, o, prov, hc, *h, url, want)
		if perr != nil {
			return aplan{}, perr
		}
		p.steps = append(p.steps, pruneSteps...)
	}
	return p, nil
}

// hookURL resolves a hook's endpoint: the flag, then the config, then the running cluster.
// proven-by: TestHookURL_FlagThenConfigThenCluster
func (o runOptions) hookURL(h HookConfig) (string, error) {
	if o.onlyURL != "" {
		return o.onlyURL, nil
	}
	if h.URL != "" {
		return h.URL, nil
	}
	if o.cluster.HookURL != "" {
		return o.cluster.HookURL, nil
	}
	return "", fmt.Errorf("hook for provider %q has no url and the cluster did not supply one: "+
		"pass -url, set url in the config, or run where the `hook` ingress exists", h.Provider)
}

// desiredRepos is the repo set the config asks for.
//
// AN EXPLICIT LIST WINS OVER the discover query rather than adding to it. Naming repos is the
// more specific statement of intent, and it is the only reading under which discover is still
// useful alongside a list: query the org to learn the SCOPE, name the repos that should have
// the hook, and let -prune remove it from the rest. Merging instead would make the list a no-op
// the moment discover matched the same org, which is not a shape anyone can reason about.
// proven-by: TestDesiredRepos_ExplicitListWinsOverDiscover
func desiredRepos(ctx context.Context, h HookConfig, hc Hooks) ([]Repo, error) {
	var explicit []Repo
	for _, rs := range h.Repos {
		repo, err := ParseRepo(rs)
		if err != nil {
			return nil, err
		}
		explicit = append(explicit, repo)
	}
	if len(explicit) > 0 || h.Discover == nil {
		return mergeRepos(explicit, nil), nil
	}
	discovered, err := discoverRepos(*h.Discover, func(owner string, isOrg bool) ([]RepoInfo, error) {
		return hc.ListRepos(ctx, owner, isOrg)
	})
	if err != nil {
		return nil, err
	}
	return mergeRepos(nil, discovered), nil
}

// prunePlan is the reconcile half: every repo in the DISCOVERED SCOPE that carries this hook's
// URL but is not in the wanted set gets a delete.
//
// The scope is the discover query and nothing else. With only an explicit repo list there is no
// "the rest" to subtract, so -prune refuses rather than guessing; and a hook whose URL differs
// is never touched, so a run cannot remove another cluster's hook.
// proven-by: TestPrunePlan_OnlyRemovesOursOutsideTheWantedSet
func prunePlan(ctx context.Context, o runOptions, prov Provider, hc Hooks, h HookConfig, url string, want []Repo) ([]step, error) {
	scope, err := discoverRepos(*h.Discover, func(owner string, isOrg bool) ([]RepoInfo, error) {
		return hc.ListRepos(ctx, owner, isOrg)
	})
	if err != nil {
		return nil, err
	}
	inScope := make(map[string]bool, len(want))
	for _, r := range want {
		inScope[strings.ToLower(r.String())] = true
	}
	var out []step
	for _, repo := range scope {
		if inScope[strings.ToLower(repo.String())] {
			continue
		}
		have, lerr := hc.ListHooks(ctx, repo)
		if lerr != nil {
			out = append(out, step{Provider: prov.Name, Repo: repo.String(), URL: url,
				Action: actionNone, Err: lerr.Error()})
			continue
		}
		if act, id := decide(have, Spec{URL: url}, false); act == actionDelete {
			out = append(out, step{Provider: prov.Name, Repo: repo.String(), URL: url,
				Action: actionDelete, HookID: id, Prune: true})
		}
	}
	return out, nil
}

// apply carries out the plan and returns what it did, with each repo's error on its own step
// so one forbidden repo does not hide the rest.
func apply(ctx context.Context, o runOptions, p aplan, deleteOnly bool) []step {
	if o.dryRun {
		return p.steps
	}
	clients := make(map[string]Hooks, len(p.provs))
	for i := range p.steps {
		st := &p.steps[i]
		prov, ok := p.provs[st.Provider]
		if !ok {
			st.Err = fmt.Sprintf("provider %q is not resolved for this run", st.Provider)
			continue
		}
		hc, ok := clients[prov.Name]
		if !ok {
			var err error
			hc, err = o.hooks(prov)
			if err != nil {
				st.Err = err.Error()
				continue
			}
			clients[prov.Name] = hc
		}
		repo, rerr := ParseRepo(st.Repo)
		if rerr != nil {
			st.Err = rerr.Error()
			continue
		}
		if werr := perform(ctx, hc, repo, st, p.specs[hookKey(st.Provider, st.URL)], deleteOnly); werr != nil {
			st.Err = werr.Error()
		}
	}
	return p.steps
}

// perform carries out one step.
func perform(ctx context.Context, hc Hooks, repo Repo, st *step, spec Spec, deleteOnly bool) error {
	switch {
	case deleteOnly && st.Action == actionDelete:
		return hc.DeleteHook(ctx, repo, st.HookID)
	case st.Action == actionCreate:
		h, err := hc.CreateHook(ctx, repo, spec)
		st.HookID = h.ID
		return err
	case st.Action == actionReplace:
		if err := hc.DeleteHook(ctx, repo, st.HookID); err != nil {
			return err
		}
		h, err := hc.CreateHook(ctx, repo, spec)
		st.HookID = h.ID
		return err
	}
	return nil
}

// command is one of the three subcommands.
type command string

const (
	// commandUpdate registers what the config describes.
	commandUpdate command = "update"
	// commandDelete removes the hooks on the configured URLs.
	commandDelete command = "delete"
	// commandList reports what is registered, changing nothing.
	commandList command = "list"
)

func main() {
	sub := commandUpdate
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = command(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("webhook", flag.ExitOnError)
	configPath := fs.String("config", "webhooks.yaml", "the config file (providers + hooks)")
	onlyProv := fs.String("provider", "", "only this provider's hooks")
	onlyRepo := fs.String("repo", "", "only this repo (owner/name), to point a run at one target")
	onlyURL := fs.String("url", "", "only the hook on this URL")
	secretEnv := fs.String("secret-env", "", "read the hook secret from this variable instead of the config")
	dryRun := fs.Bool("dry-run", false, "print what would change, change nothing")
	expectNoChange := fs.Bool("expect-no-change", false, "fail if the plan is not empty")
	warnOnFail := fs.Bool("warn-on-fail", false, "report a failure and keep going (exit 0)")
	asJSON := fs.Bool("json", false, "write the result as JSON")
	prune := fs.Bool("prune", false, "also remove this hook's URL from repos in the discover scope that the config does not name")
	namespace := fs.String("namespace", defaultDevNamespace, "namespace holding the `hook` ingress and the HMAC secret")
	noCluster := fs.Bool("no-cluster", false, "never read the cluster: the config must carry url and secret")
	_ = fs.Parse(args)

	switch sub {
	case commandUpdate, commandDelete, commandList:
	default:
		fmt.Fprintf(os.Stderr, "webhook: unknown command %q (want update, delete or list)\n", sub)
		os.Exit(2)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	o := runOptions{cfg: cfg, onlyProv: *onlyProv, onlyRepo: *onlyRepo, onlyURL: *onlyURL,
		dryRun: *dryRun, warnOnFail: *warnOnFail, asJSON: *asJSON, getenv: os.Getenv, hooks: NewHooks,
		prune: *prune}
	if !*noCluster {
		o.cluster = readCluster(*namespace)
	}
	if *secretEnv != "" {
		v := os.Getenv(*secretEnv)
		if v == "" {
			fmt.Fprintf(os.Stderr, "FAIL: -secret-env %s is set but the variable is empty\n", *secretEnv)
			os.Exit(1)
		}
		o.envSecret = v
	}

	for _, e := range o.cluster.Errors {
		fmt.Fprintf(os.Stderr, "note: %s\n", e)
	}
	if o.cluster.HookURL != "" && !*asJSON {
		fmt.Printf("cluster: hook endpoint %s (ingress %s)\n", o.cluster.HookURL, o.cluster.Ingress)
	}
	if *prune && o.onlyRepo != "" {
		fmt.Fprintln(os.Stderr, "note: -prune with -repo prunes within that one repo's scope only")
	}

	ctx := context.Background()
	deleteOnly := sub == commandDelete
	p, perr := plan(ctx, o, deleteOnly)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", perr)
		os.Exit(1)
	}
	if sub == commandList {
		report(p.steps, *asJSON, true, true)
		return
	}
	if o.dryRun {
		report(p.steps, *asJSON, false, true)
		if *expectNoChange && changed(p.steps, deleteOnly) {
			fmt.Fprintln(os.Stderr, "FAIL: -expect-no-change, but the plan is not empty")
			os.Exit(1)
		}
		return
	}
	if *expectNoChange && changed(p.steps, deleteOnly) {
		report(p.steps, *asJSON, false, true)
		fmt.Fprintln(os.Stderr, "FAIL: -expect-no-change, but the plan is not empty")
		os.Exit(1)
	}
	done := apply(ctx, o, p, deleteOnly)
	report(done, *asJSON, false, false)

	failed := 0
	for _, s := range done {
		if s.Err != "" {
			failed++
		}
	}
	if failed > 0 && !*warnOnFail {
		os.Exit(1)
	}
}

// changed reports whether the plan would touch a repo. An error counts as a change: a run that
// could not read a repo has not established that it is correct.
func changed(steps []step, deleteOnly bool) bool {
	for _, s := range steps {
		if s.Err != "" {
			return true
		}
		if !deleteOnly && (s.Action == actionCreate || s.Action == actionReplace) {
			return true
		}
		if deleteOnly && s.Action == actionDelete {
			return true
		}
	}
	return false
}

// report prints the steps. A listing is sorted so two runs over the same forge diff cleanly;
// `planned` says whether anything was actually done, so a dry run and a real run do not
// print the same summary for the same word.
func report(steps []step, asJSON, listing, planned bool) {
	if asJSON {
		b, _ := json.MarshalIndent(steps, "", "  ")
		fmt.Println(string(b))
		return
	}
	if listing {
		sort.Slice(steps, func(i, j int) bool {
			if steps[i].Provider != steps[j].Provider {
				return steps[i].Provider < steps[j].Provider
			}
			return steps[i].Repo < steps[j].Repo
		})
	}
	for _, s := range steps {
		line := fmt.Sprintf("%-9s %-8s %-40s %s", s.Action, s.Provider, s.Repo, s.URL)
		if s.HookID != 0 {
			line += fmt.Sprintf(" (id %d)", s.HookID)
		}
		if len(s.Other) > 0 && s.Action != actionNone {
			line += "\n          WARN: the repo already has a hook on " + strings.Join(s.Other, ", ")
		}
		if s.Err != "" {
			line += "\n          FAIL: " + s.Err
		}
		fmt.Println(line)
	}
	counts := map[action]int{}
	for _, s := range steps {
		counts[s.Action]++
	}
	verb := "done"
	if planned {
		verb = "planned, nothing written"
	}
	fmt.Printf("==> %d repo(s) %s: %d created, %d replaced, %d already present, %d removed\n",
		len(steps), verb, counts[actionCreate], counts[actionReplace], counts[actionNone], counts[actionDelete])
}
