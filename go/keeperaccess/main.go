// keeperaccess — a repository in the Tide query that the keeper lacks write
// access to is a repository whose PRs go green and then sit there forever.
//
// WHY THIS EXISTS. On 2026-11-15 `leartech-forge-testbed` PR#3 passed every
// check and did not merge. The keeper's log said, once a minute:
//
//	Merge failed.: Not Found                      merge-targets=[3]
//	Failed to set status context from "": Not Found
//	Error syncing subpool: failed merging [3]: [Not Found]
//
// Reads worked — the pool was built, the PR was seen, the checks were seen — and
// every WRITE was a 404. The credential is a classic personal access token
// owned by `LeartechKeeperBot`, so it reaches a repository only when that user
// does, and that user gets access by being INVITED AS A COLLABORATOR per repo.
// Nobody had invited it to the repos created since the last time somebody did.
//
// The asymmetry is what makes it invisible. A PUBLIC repo the bot is not a
// collaborator on is still readable (`pull: true`), so the keeper builds a pool
// and looks healthy; only the write fails, and the message is a bare "Not
// Found" with no repo name in the interesting half. A PRIVATE repo it has no
// access to answers 404 on every sync for as long as it is registered —
// `jx-prod-cluster-gsm` had been in that state since August.
//
// THE RULE. Every repository in the keeper's Tide query must grant the keeper's
// bot write access; anything else is a registered repo the keeper will not merge.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not check branch protection, the
// required contexts, or whether the bot can merge specifically (that depends on
// protection rules and is a per-repo decision). It checks only that the
// credential can write at all, which is the precondition for all of it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// repoStatus is what the forge says about one repository, reduced to the two
// facts that decide this: can the credential see it, and can it write.
type repoStatus struct {
	Repo   string
	Exists bool
	Push   bool
}

type options struct {
	configPath string
	server     string
	token      string
	timeout    time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.configPath, "config", "", "path to a Lighthouse config.yaml holding the Tide query")
	flag.StringVar(&o.server, "server", "", "forge base URL, e.g. https://github.com (default: $GIT_SERVER)")
	flag.StringVar(&o.token, "token", "", "token for the forge (default: $GIT_TOKEN)")
	flag.DurationVar(&o.timeout, "timeout", 30*time.Second, "per-request timeout")
	flag.Parse()

	if o.server == "" {
		o.server = os.Getenv("GIT_SERVER")
	}
	if o.token == "" {
		o.token = os.Getenv("GIT_TOKEN")
	}
	fail := func(format string, a ...interface{}) {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		os.Exit(2)
	}
	if o.configPath == "" {
		fail("keeperaccess: -config is required")
	}
	if o.server == "" {
		fail("keeperaccess: no forge URL: pass -server or set $GIT_SERVER")
	}
	if o.token == "" {
		fail("keeperaccess: no token: pass -token or set $GIT_TOKEN. Without one every\n" +
			"repository would report as unreachable, which is not a finding.")
	}
	raw, err := os.ReadFile(o.configPath)
	if err != nil {
		fail("keeperaccess: cannot read %s: %v", o.configPath, err)
	}
	repos, err := TideQueryRepos(string(raw))
	if err != nil {
		fail("keeperaccess: %s: %v", o.configPath, err)
	}
	if len(repos) == 0 {
		fail("keeperaccess: the Tide query in %s lists no repositories. An empty check\n"+
			"would pass over every repo without looking at one.", o.configPath)
	}

	client := &http.Client{Timeout: o.timeout}
	results := checkAll(client, o.server, o.token, repos)

	var broken []repoStatus
	for _, r := range results {
		if !r.Exists || !r.Push {
			broken = append(broken, r)
		}
	}
	fmt.Printf("==> keeperaccess: %d repository(ies) in the Tide query, %d the bot cannot write to\n",
		len(results), len(broken))
	for _, r := range broken {
		switch {
		case !r.Exists:
			fmt.Printf("  NOT VISIBLE  %s\n", r.Repo)
		default:
			fmt.Printf("  READ ONLY    %s\n", r.Repo)
		}
	}
	if len(broken) > 0 {
		fmt.Println()
		fmt.Println("A repository in the Tide query that the keeper's credential cannot write to is")
		fmt.Println("one whose PRs are checked and then never merged: the status write and the merge")
		fmt.Println("are both refused, while every read keeps working so the keeper looks healthy.")
		fmt.Println()
		fmt.Println("The keeper token is a personal access token on the bot account, so it reaches")
		fmt.Println("a repo only when the bot is a collaborator on it. Grant write:")
		fmt.Println()
		fmt.Println("  gh api -X PUT repos/<owner>/<repo>/collaborators/<bot> -f permission=push")
		fmt.Println()
		fmt.Println("then accept the invitation as the bot, which a user-owned repo requires:")
		fmt.Println()
		fmt.Println("  gh api /user/repository_invitations            # as the bot, to find the id")
		fmt.Println("  gh api -X PATCH /user/repository_invitations/<id>")
		fmt.Println()
		fmt.Println("A private repo the bot cannot see is the same defect one step further on.")
		os.Exit(1)
	}
	fmt.Println("==> keeperaccess: ok")
}

// checkAll queries every repository, in parallel because a slow forge should not
// make this take a minute per repo.
func checkAll(client *http.Client, server, token string, repos []string) []repoStatus {
	out := make([]repoStatus, len(repos))
	done := make(chan int, len(repos))
	for i, r := range repos {
		go func(i int, repo string) {
			out[i] = checkOne(client, server, token, repo)
			done <- i
		}(i, r)
	}
	for range repos {
		<-done
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// apiBase returns the REST prefix for a forge.
//
// GitHub's API is a DIFFERENT HOST from its web UI, which is the difference this
// got wrong first: building github.com/repos/... instead of
// api.github.com/repos/... made every repository 404, and the checker then
// reported all 82 as invisible — a checker failing in the direction that looks
// like the worst possible finding.
func apiBase(server string) string {
	s := strings.TrimSuffix(server, "/")
	if strings.Contains(s, "api.github.com") {
		return s
	}
	if strings.Contains(s, "github.com") {
		return strings.Replace(s, "github.com", "api.github.com", 1)
	}
	return s + "/api/v1"
}

// checkOne asks the forge for one repository and reduces the answer to the two
// facts that decide this check.
//
// 404 is not an error here: for a private repo the credential has no access to,
// it is the whole finding, and reporting it as a request failure would hide it
// behind a tooling message.
func checkOne(client *http.Client, server, token, repo string) repoStatus {
	st := repoStatus{Repo: repo}
	url := apiBase(server) + "/repos/" + repo
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return st
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return st
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return st
	}
	if resp.StatusCode != http.StatusOK {
		return st
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return st
	}
	var payload struct {
		Permissions map[string]bool `json:"permissions"`
		// gitea reports the same fact as a top-level boolean.
		Permission string `json:"permission"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return st
	}
	st.Exists = true
	st.Push = payload.Permissions["push"] || payload.Permission == "write" ||
		payload.Permission == "admin" || payload.Permission == "owner"
	return st
}

// TideQueryRepos returns every repository named in the config's Tide queries.
//
// Parsed line by line rather than through a YAML library: the checkers image
// carries no YAML package, and the shape here is a list of scalars under a
// `repos:` key. Anything that is not a plain `owner/name` is skipped rather
// than guessed at, so a config change that moves the query somewhere this does
// not understand shows up as a repo count that did not move — which is why the
// caller refuses an empty list.
func TideQueryRepos(config string) ([]string, error) {
	seen := map[string]bool{}
	var repos []string
	inQueries := false
	// inRepos is true only while reading the items that follow a `repos:` key.
	// It ends at the next key — including one written as a list item, which is
	// how a query's own `labels:` appears — because a labels list holds things
	// like `env/staging` that would otherwise be collected as repositories.
	inRepos := false

	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(line)
		item := strings.TrimPrefix(trimmed, "- ")

		switch {
		case trimmed == "queries:":
			inQueries, inRepos = true, false
			continue
		case !inQueries:
			continue
		case item == "repos:":
			inRepos = true
			continue
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasSuffix(item, ":"):
			// A key, so the repo list above it is over. `queries:` above is
			// handled first; a nested key like `labels:` lands here.
			inRepos = false
			continue
		}

		if !inRepos || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		repo := strings.Trim(item, `"'`)
		if !strings.Contains(repo, "/") || strings.ContainsAny(repo, " :") {
			continue
		}
		if !seen[repo] {
			seen[repo] = true
			repos = append(repos, repo)
		}
	}
	sort.Strings(repos)
	return repos, nil
}
