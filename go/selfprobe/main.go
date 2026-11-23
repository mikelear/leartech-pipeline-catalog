// selfprobe — open a REAL pull request that FAILS, and require the catalogue to post the comment
// that explains why.
//
// WHY. On 2026-11-23 `leartech-console-api#37` showed `gcp/lint` red with no comment: the report
// step that exists to post the reason died on an undeclared `${CLUSTER_ID:?}`, in EIGHTEEN tasks,
// and none of those report steps had ever posted anything. From the forge that is
// indistinguishable from "prpost could not post" — a red check with no comment looks the same
// either way, and the only evidence is a pod log nobody was told to open.
//
// Every other guard answers "does the tool work?" or "is it called?". None answered "when this
// pipeline fails on a real PR, does the explanation arrive?", which is what the whole comment
// layer exists for. This asks it by making one fail on purpose: commit a failing test, open a
// pull request, wait for the check to go RED, require the marker comment within a grace window,
// then close the pull request and delete the branch.
//
// The two failures are different facts and say different things:
//
//	the check never went red        -> the probe or the trigger is broken
//	the check went red, no comment  -> THE ESTATE IS BROKEN; that is console-api#31
//
// Usage (the token needs repo write on the probe repo, and comes from the environment so it never
// appears in a pod spec or a flag):
//
//	selfprobe -forge https://forgejo.jx.leartech.com -repo it-test/lh-test \
//	          -check test -marker leartech-coverage
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const probeFile = "selfprobe_deliberate_failure_test.go"

const probeSource = `package main

import "testing"

// Written by the pipeline self-probe. This test MUST fail: the point of the probe is a check
// that goes red, so the comment that explains a red check has something to explain.
func TestSelfProbeDeliberateFailure(t *testing.T) {
	t.Fatal("selfprobe: this failure is deliberate — it exists so the report step has to post a comment")
}
`

type client struct {
	base  string
	token string
	http  *http.Client
	repo  string // owner/name
}

func (c *client) do(method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+"/api/v1/repos/"+c.repo+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type pull struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
	Head   struct {
		Sha string `json:"sha"`
	} `json:"head"`
}

type comment struct {
	Body string `json:"body"`
}

type combinedStatus struct {
	Statuses []struct {
		Context string `json:"context"`
		Status  string `json:"status"`
	} `json:"statuses"`
}

type branch struct {
	Commit struct {
		ID string `json:"id"`
	} `json:"commit"`
}

// What this run created, so a failure can undo it. Package-level because fail() runs from the
// bottom of the wait loops, where there is no call stack left to thread them through.
var (
	probeForge  string
	probeRepo   string
	probeBranch string
	probePR     int
	keepOpen    bool
)

func main() {
	forge := flag.String("forge", "", "forge base URL, e.g. https://forgejo.jx.leartech.com")
	repo := flag.String("repo", "", "probe repo as owner/name, e.g. it-test/lh-test")
	base := flag.String("base", "main", "base branch")
	check := flag.String("check", "test", "the status context that must go red")
	marker := flag.String("marker", "leartech-coverage", "the sticky marker the comment must carry")
	timeout := flag.Duration("timeout", 20*time.Minute, "how long to wait for the check to report")
	grace := flag.Duration("grace", 4*time.Minute, "how long a red check may exist without its comment")
	poll := flag.Duration("poll", 15*time.Second, "poll interval")
	keep := flag.Bool("keep", false, "leave the pull request open for inspection instead of cleaning up")
	flag.Parse()

	token := strings.TrimSpace(os.Getenv("FORGE_TOKEN"))
	if *forge == "" || *repo == "" || token == "" {
		fmt.Fprintln(os.Stderr, "selfprobe: -forge, -repo and $FORGE_TOKEN are required")
		os.Exit(2)
	}

	c := &client{base: strings.TrimRight(*forge, "/"), token: token,
		http: &http.Client{Timeout: 30 * time.Second}, repo: *repo}

	stamp := time.Now().UTC().Format("20060102-150405")
	branchName := "selfprobe/" + stamp
	probeForge, probeRepo, probeBranch, keepOpen = *forge, *repo, branchName, *keep

	var b branch
	if err := c.do(http.MethodGet, "/branches/"+*base, nil, &b); err != nil {
		fail("read base branch %q: %v", *base, err)
	}
	fmt.Printf("== selfprobe: %s @ %s -> branch %s\n", *repo, short(b.Commit.ID), branchName)

	// `new_branch` alone creates the branch; sending `branch` as well makes Forgejo look for a
	// branch that does not exist yet and answer 404 (measured on 15.0.10, first live run).
	if err := c.do(http.MethodPost, "/contents/"+probeFile, map[string]any{
		"new_branch": branchName,
		"message": "selfprobe: a deliberate failure, so a red check has something to explain\n\n" +
			stamp,
		"content": base64.StdEncoding.EncodeToString([]byte(probeSource)),
	}, nil); err != nil {
		fail("commit the failing file: %v", err)
	}

	var pr pull
	if err := c.do(http.MethodPost, "/pulls", map[string]any{
		"head":  branchName,
		"base":  *base,
		"title": "selfprobe " + stamp + ": a check that must fail, and say why",
		"body": "Written by `selfprobe`, the pipeline's end-to-end self-test.\n\nThis pull request " +
			"exists so that a check goes RED on a real forge, and the comment that explains it has " +
			"to arrive. It is closed automatically; if you are reading it, the probe is running.\n",
	}, &pr); err != nil {
		fail("open the pull request: %v", err)
	}
	probePR = pr.Number
	fmt.Printf("== opened %s#%d\n", *repo, pr.Number)

	defer func() {
		if *keep {
			fmt.Printf("== -keep: leaving %s#%d open\n", *repo, pr.Number)
			return
		}
		_ = c.do(http.MethodPatch, fmt.Sprintf("/pulls/%d", pr.Number),
			map[string]any{"state": "closed"}, nil)
		_ = c.do(http.MethodDelete, "/branches/"+branchName, nil, nil)
		fmt.Printf("== closed %s#%d and deleted %s\n", *repo, pr.Number, branchName)
	}()

	deadline := time.Now().Add(*timeout)
	var seen []string
	var redAt time.Time
	for redAt.IsZero() {
		seen = statuses(c, pr.Number)
		if s := statusOf(seen, *check); s == "failure" || s == "error" {
			redAt = time.Now()
			break
		}
		if time.Now().After(deadline) {
			fail("the %q check on %s#%d never went red within %s (saw: %s).\n"+
				"     The probe committed a test that fails, so either the trigger did not fire or "+
				"the probe itself is broken — this is NOT a comment-delivery failure.",
				*check, *repo, pr.Number, *timeout, strings.Join(seen, ", "))
		}
		time.Sleep(*poll)
	}
	fmt.Printf("== %q went red on %s#%d (as it must)\n", *check, *repo, pr.Number)

	for {
		if body := findComment(c, pr.Number, *marker); body != "" {
			fmt.Printf("== the report step posted its comment (%d bytes, marker %q)\n",
				len(body), *marker)
			fmt.Println("== OK: a red check on a real pull request explained itself")
			return
		}
		if time.Since(redAt) > *grace {
			fail("the %q check on %s#%d went red %s ago and no comment carrying %q has appeared.\n"+
				"     That is console-api#31 on a real forge: a red check whose only message is "+
				"Lighthouse's generic table. Read the report step's log — this catalogue has "+
				"shipped the class twice (an undeclared ${VAR:?} that exits before prpost runs, and "+
				"a report step with no onError: continue).\n     %s/%s/pulls/%d",
				*check, *repo, pr.Number, *grace, *marker, *forge, *repo, pr.Number)
		}
		time.Sleep(*poll)
	}
}

// statuses reads the combined status of the pull request's HEAD commit — Lighthouse writes one
// status per check, so this is where a red check appears.
func statuses(c *client, number int) []string {
	var pr pull
	if err := c.do(http.MethodGet, fmt.Sprintf("/pulls/%d", number), nil, &pr); err != nil {
		return nil
	}
	if pr.Head.Sha == "" {
		return nil
	}
	var cs combinedStatus
	if err := c.do(http.MethodGet, "/commits/"+pr.Head.Sha+"/status", nil, &cs); err != nil {
		return nil
	}
	seen := make([]string, 0, len(cs.Statuses))
	for _, st := range cs.Statuses {
		seen = append(seen, st.Context+"="+st.Status)
	}
	return seen
}

// statusOf returns the state of one context, matching on the last path element as well because
// the estate's checks are cluster-prefixed (`gcp/test`, `az/test`). Equality alone made the probe
// report "never went red" while `gcp/test=failure` was in the list it printed.
func statusOf(seen []string, context string) string {
	for _, entry := range seen {
		ctx, state, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if ctx == context || strings.HasSuffix(ctx, "/"+context) {
			return state
		}
	}
	return ""
}

func findComment(c *client, number int, marker string) string {
	var cs []comment
	if err := c.do(http.MethodGet, fmt.Sprintf("/issues/%d/comments", number), nil, &cs); err != nil {
		return ""
	}
	for _, cm := range cs {
		if strings.Contains(cm.Body, marker) {
			return cm.Body
		}
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// fail reports the failure and exits — but not before tidying up after itself.
//
// os.Exit skips deferred calls, so the first version of this left a pull request OPEN and its
// branch behind on every failure (measured: it-test/lh-test#18). A canary that fills the repo it
// watches with broken pull requests is one whose findings nobody can see past, which is the
// opposite of the point.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\nFAIL selfprobe: "+format+"\n", args...)
	cleanupNow()
	os.Exit(1)
}

// cleanupNow closes the probe pull request and deletes its branch, if this run created one and
// -keep was not asked for. Best effort and silent on its own errors: it runs on the way out of a
// failure, and a second error there would bury the first.
func cleanupNow() {
	if keepOpen || probePR == 0 {
		return
	}
	c := &client{
		base:  probeForge,
		token: strings.TrimSpace(os.Getenv("FORGE_TOKEN")),
		http:  &http.Client{Timeout: 20 * time.Second},
		repo:  probeRepo,
	}
	_ = c.do(http.MethodPatch, fmt.Sprintf("/pulls/%d", probePR),
		map[string]any{"state": "closed"}, nil)
	_ = c.do(http.MethodDelete, "/branches/"+probeBranch, nil, nil)
	fmt.Fprintf(os.Stderr, "   (closed %s#%d and deleted %s)\n", probeRepo, probePR, probeBranch)
}
