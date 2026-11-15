package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tideConfig = `tide:
  context_options:
    from-branch-protection: true
  queries:
  - labels:
    - approved
    missingLabels:
    - do-not-merge
    repos:
    - mikelear/leartech-console-api
    - mikelear/leartech-forge-testbed
  - labels:
    - env/staging
    repos:
    - mikelear/jx-prod-cluster-gsm
  merge_method:
    mikelear/leartech-console-api: merge
    mikelear/only-in-merge-method: merge
`

// The parse is the part that degrades silently: a config reshuffle that moved
// the repo list out of the shape this understands would yield zero repos and a
// green check that looked at nothing.
func TestTideQueryReposReadsEveryQuery(t *testing.T) {
	got, err := TideQueryRepos(tideConfig)
	if err != nil {
		t.Fatalf("TideQueryRepos: %v", err)
	}
	want := []string{
		"mikelear/jx-prod-cluster-gsm",
		"mikelear/leartech-console-api",
		"mikelear/leartech-forge-testbed",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("repos = %v, want %v", got, want)
	}
}

// merge_method is a sibling map keyed the same way. Counting its keys would make
// a repository nobody polls look checked.
func TestTideQueryReposIgnoresSiblingMaps(t *testing.T) {
	got, _ := TideQueryRepos(tideConfig)
	for _, r := range got {
		if r == "mikelear/only-in-merge-method" {
			t.Fatal("merge_method keys are not in the query and must not be checked")
		}
	}
}

func TestTideQueryReposDeduplicates(t *testing.T) {
	cfg := "queries:\n- repos:\n  - a/b\n  - a/b\n  - a/b\n"
	if got, _ := TideQueryRepos(cfg); len(got) != 1 {
		t.Errorf("got %v, want one entry", got)
	}
}

func TestTideQueryReposEmptyWhenNoQueries(t *testing.T) {
	if got, _ := TideQueryRepos("tide:\n  merge_method:\n    a/b: merge\n"); len(got) != 0 {
		t.Errorf("got %v, want none — the caller refuses an empty list", got)
	}
}

func stub(t *testing.T, routes map[string]int, bodies map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(code)
		if b, ok := bodies[r.URL.Path]; ok {
			_, _ = w.Write([]byte(b))
		}
	}))
}

// The finding this checker exists for: a public repo the bot can READ but not
// WRITE. Every read succeeds, so a check that only asked "can I see it" passes
// while the PR sits unmerged — which is what happened on 2026-11-15.
func TestCheckOneReadableButNotWritableIsAFinding(t *testing.T) {
	srv := stub(t,
		map[string]int{"/api/v1/repos/o/readonly": http.StatusOK},
		map[string]string{"/api/v1/repos/o/readonly": `{"permissions":{"pull":true,"push":false}}`})
	defer srv.Close()

	st := checkOne(srv.Client(), srv.URL, "tok", "o/readonly")
	if !st.Exists {
		t.Fatal("a 200 means the repository is visible")
	}
	if st.Push {
		t.Error("push:false must be reported as not writable; accepting it is the bug")
	}
}

func TestCheckOneWritableIsNotAFinding(t *testing.T) {
	srv := stub(t,
		map[string]int{"/api/v1/repos/o/rw": http.StatusOK},
		map[string]string{"/api/v1/repos/o/rw": `{"permissions":{"pull":true,"push":true}}`})
	defer srv.Close()

	if st := checkOne(srv.Client(), srv.URL, "tok", "o/rw"); !st.Exists || !st.Push {
		t.Errorf("got %+v, want visible and writable", st)
	}
}

// A private repo the credential cannot see answers 404 for reads and writes
// alike, and has to be reported as invisible rather than folded into a request
// error — that is how jx-prod-cluster-gsm stayed unnoticed for six days.
func TestCheckOneNotFoundIsInvisibleNotAnError(t *testing.T) {
	srv := stub(t, map[string]int{}, nil)
	defer srv.Close()

	st := checkOne(srv.Client(), srv.URL, "tok", "o/private")
	if st.Exists || st.Push {
		t.Errorf("got %+v, want invisible and not writable", st)
	}
}

// Forgejo reports the same fact as one permission string rather than a map, so
// the forge-side repos must be judged by the same rule.
func TestCheckOneForgejoPermissionString(t *testing.T) {
	for perm, want := range map[string]bool{"write": true, "read": false, "": false} {
		srv := stub(t,
			map[string]int{"/api/v1/repos/o/f": http.StatusOK},
			map[string]string{"/api/v1/repos/o/f": `{"permission":"` + perm + `"}`})

		if st := checkOne(srv.Client(), srv.URL, "tok", "o/f"); !st.Exists || st.Push != want {
			t.Errorf("permission=%q: got %+v, want visible push=%v", perm, st, want)
		}
		srv.Close()
	}
}

// GitHub's API is a different host from its web UI. Building github.com/repos/…
// made every repository 404 and the checker reported all 82 as invisible — a
// checker failing in the direction that looks like the worst possible finding,
// which is worse than one that fails loudly.
func TestAPIBase(t *testing.T) {
	cases := []struct{ server, want string }{
		{"https://github.com", "https://api.github.com"},
		{"https://github.com/", "https://api.github.com"},
		{"https://forgejo.jx.leartech.com", "https://forgejo.jx.leartech.com/api/v1"},
		{"https://api.github.com", "https://api.github.com"},
	}
	for _, c := range cases {
		if got := apiBase(c.server); got != c.want {
			t.Errorf("apiBase(%q) = %q, want %q", c.server, got, c.want)
		}
	}
}
