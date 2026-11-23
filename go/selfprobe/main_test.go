package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeForge answers the five endpoints selfprobe uses, and can be told what the check does and
// whether a comment exists — so both the passing and the failing story are testable without a
// Forgejo.
type fakeForge struct {
	mu         sync.Mutex
	checkState string // "pending" | "failure" | "success"
	comment    string // body of the one comment, "" for none
	prOpen     bool
	branchGone bool
	created    []string
}

func (f *fakeForge) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.created = append(f.created, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/repos/it-test/lh-test/branches/main"):
			fmt.Fprint(w, `{"commit":{"id":"0123456789abcdef"}}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/contents/"):
			fmt.Fprint(w, `{"content":{}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
			f.prOpen = true
			fmt.Fprint(w, `{"number":7,"state":"open","title":"selfprobe"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/7"):
			fmt.Fprint(w, `{"number":7,"state":"open","head":{"sha":"deadbeef"}}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/commits/deadbeef/status"):
			fmt.Fprintf(w, `{"statuses":[{"context":"gcp/test","status":%q}]}`, f.checkState)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			if f.comment == "" {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"body":%q}]`, f.comment)
		case r.Method == http.MethodPatch:
			f.prOpen = false
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodDelete:
			f.branchGone = true
			fmt.Fprint(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"not found"}`)
		}
	})
}

func serve(t *testing.T, f *fakeForge) *client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &client{base: srv.URL, token: "tok", http: srv.Client(), repo: "it-test/lh-test"}
}

// THE PROPERTY: a red check must be followed by its comment. If it is not, that is
// console-api#31 on a real forge — the failure the whole comment layer exists to prevent.
//
// proven-by: TestSelfProbe_RedCheckWithoutACommentIsAFailure
func TestSelfProbe_RedCheckWithoutACommentIsAFailure(t *testing.T) {
	f := &fakeForge{checkState: "failure"} // red, and nothing posted
	c := serve(t, f)

	// The two halves the probe compares, in the order it compares them.
	seen := statuses(c, 7)
	if got := statusOf(seen, "test"); got != "failure" {
		t.Fatalf("statusOf = %q, want failure from %v", got, seen)
	}
	if body := findComment(c, 7, "leartech-coverage"); body != "" {
		t.Fatalf("findComment = %q, want empty — this case is the defect", body)
	}
}

// ...and the same red check WITH its comment is the pass. Both directions, because a probe that
// can only fail is as useless as one that can only pass.
//
// proven-by: TestSelfProbe_RedCheckWithItsCommentPasses
func TestSelfProbe_RedCheckWithItsCommentPasses(t *testing.T) {
	f := &fakeForge{checkState: "failure", comment: "<!-- leartech-coverage-gcp -->\n2 of 3"}
	c := serve(t, f)
	if body := findComment(c, 7, "leartech-coverage"); body == "" {
		t.Fatal("findComment found nothing for the marker the report step posts")
	}
}

// A comment carrying a DIFFERENT marker is not this check's comment. The marker is the sticky
// key, so another tool's comment must not satisfy the probe.
//
// proven-by: TestFindCommentIgnoresAnotherToolsMarker
func TestFindCommentIgnoresAnotherToolsMarker(t *testing.T) {
	f := &fakeForge{checkState: "failure", comment: "<!-- leartech-lint-gcp --> unrelated"}
	c := serve(t, f)
	if body := findComment(c, 7, "leartech-coverage"); body != "" {
		t.Fatalf("findComment matched another tool's marker: %q", body)
	}
}

// The estate's checks are cluster-prefixed. Equality alone reported "never went red" while
// `gcp/test=failure` was in the list the probe printed — measured on its first live run.
//
// proven-by: TestStatusOfMatchesAClusterPrefixedContext
func TestStatusOfMatchesAClusterPrefixedContext(t *testing.T) {
	for _, tc := range []struct {
		seen []string
		want string
	}{
		{[]string{"gcp/test=failure"}, "failure"},
		{[]string{"az/test=success"}, "success"},
		{[]string{"test=failure"}, "failure"},
		{[]string{"gcp/test=failure", "pr=success"}, "failure"},
		{[]string{"gcp/not-test=failure"}, ""},
		{[]string{"gcp/test"}, ""}, // a malformed entry is not a state
		{[]string{}, ""},
	} {
		if got := statusOf(tc.seen, "test"); got != tc.want {
			t.Errorf("statusOf(%v, test) = %q, want %q", tc.seen, got, tc.want)
		}
	}
}

// The probe's own plumbing: it must close the pull request and delete the branch, or every run
// leaves litter on the canary repo — and a canary repo full of open PRs is one nobody reads.
//
// proven-by: TestSelfProbe_CleansUpThePullRequestAndBranch
func TestSelfProbe_CleansUpThePullRequestAndBranch(t *testing.T) {
	f := &fakeForge{checkState: "failure", comment: "<!-- leartech-coverage-gcp --> ok"}
	c := serve(t, f)

	// The same calls main() makes, without its 20-minute loops.
	if err := c.do(http.MethodGet, "/branches/main", nil, nil); err != nil {
		t.Fatalf("read base branch: %v", err)
	}
	if err := c.do(http.MethodPost, "/contents/x.go", map[string]any{"new_branch": "selfprobe/t"}, nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := c.do(http.MethodPatch, "/pulls/7", map[string]any{"state": "closed"}, nil); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := c.do(http.MethodDelete, "/branches/selfprobe/t", nil, nil); err != nil {
		t.Fatalf("delete branch: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prOpen {
		t.Error("the pull request was left open")
	}
	if !f.branchGone {
		t.Error("the branch was left behind")
	}
}

// A 4xx from the forge must be an error, not a silently empty answer — otherwise the probe reads
// "no comment yet" for a token that cannot list comments at all, and reports the WRONG failure.
//
// proven-by: TestClientSurfacesAForgeError
func TestClientSurfacesAForgeError(t *testing.T) {
	f := &fakeForge{checkState: "pending"}
	c := serve(t, f)
	err := c.do(http.MethodGet, "/does-not-exist", nil, nil)
	if err == nil {
		t.Fatal("a 404 was returned as success")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q does not carry the status", err)
	}
}

// A failure must tidy up after itself.
//
// fail() used to call os.Exit, which SKIPS deferred calls, so every failure left its pull request
// open and its branch behind — measured: it-test/lh-test#18 and selfprobe/20261123-182033 survived
// a failed run. A canary that fills the repo it watches with broken pull requests is one whose
// findings nobody can see past.
//
// proven-by: TestCleanupNowClosesTheProbePullRequest
func TestCleanupNowClosesTheProbePullRequest(t *testing.T) {
	f := &fakeForge{checkState: "failure"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	// The state a mid-run failure leaves behind.
	probeForge, probeRepo = srv.URL, "it-test/lh-test"
	probeBranch, probePR, keepOpen = "selfprobe/t", 7, false
	t.Setenv("FORGE_TOKEN", "tok")
	defer func() { probePR, probeBranch, keepOpen = 0, "", false }()

	cleanupNow()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prOpen {
		t.Error("the probe pull request was left open after a failure")
	}
	if !f.branchGone {
		t.Error("the probe branch was left behind after a failure")
	}
}

// ...and -keep is respected, because a person debugging the probe needs the pull request to stay.
//
// proven-by: TestCleanupNowHonoursKeep
func TestCleanupNowHonoursKeep(t *testing.T) {
	f := &fakeForge{checkState: "failure"}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()

	probeForge, probeRepo = srv.URL, "it-test/lh-test"
	probeBranch, probePR, keepOpen = "selfprobe/t", 7, true
	t.Setenv("FORGE_TOKEN", "tok")
	defer func() { probePR, probeBranch, keepOpen = 0, "", false }()

	cleanupNow()

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.branchGone {
		t.Error("-keep was ignored: the branch was deleted anyway")
	}
}
