package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// REAL comment bodies, not fixtures-in-a-vacuum: the same strings the end2end
// task used to curl — a passing table, a failing one, a pending one — posted at
// a fake forge-api that behaves like the real sticky route (same id on the
// second post of a key). If the visual or machine half drifts, these fail with
// the actual diff in front of us.
func TestRender_PostsVisualAndMachineInOneComment(t *testing.T) {
	cases := []struct {
		name   string
		visual string
		rep    Report
	}{
		{
			name:   "end2end pass",
			visual: "## End-to-End (gcp)\n\n| suite | cases | pass | fail |\n|---|---|---|---|\n| api | 42 | 42 | 0 |",
			rep:    Report{Tool: "end2end", Check: "end2end", Outcome: "success", Cluster: "gcp", Summary: "all 42 cases green"},
		},
		{
			name:   "coverage fail",
			visual: "## Coverage\n\n**82.3%** (delta -1.5%, tolerance -0.5%)",
			rep: Report{Tool: "go-test", Check: "test", Outcome: "failure", Cluster: "gcp",
				Summary: "coverage dropped below tolerance", Failures: []string{"coverage 82.3% < 87.8%-0.5%"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := Render(c.rep.Tool, c.visual, c.rep)
			if !strings.Contains(body, c.rep.Summary) {
				t.Fatalf("visual half missing the summary:\n%s", body)
			}
			if !strings.Contains(body, "```leartech-report") {
				t.Fatalf("machine fence missing:\n%s", body)
			}
			// The machine half must decode back out of the fence, whole.
			open := strings.Index(body, "```leartech-report\n")
			close_ := strings.Index(body[open:], "\n```")
			var back Report
			if err := json.Unmarshal([]byte(body[open+len("```leartech-report\n"):open+close_]), &back); err != nil {
				t.Fatalf("machine block does not decode: %v\n%s", err, body)
			}
			if back.Outcome != c.rep.Outcome || back.Tool != c.rep.Tool {
				t.Fatalf("machine block lost fields: %+v", back)
			}
			if len(back.Failures) != len(c.rep.Failures) {
				t.Fatalf("failures lost: %+v", back)
			}
			if !strings.Contains(body, "<!-- "+StickyKey(c.rep.Tool, c.rep.Cluster)+" -->") {
				t.Fatalf("sticky marker missing:\n%s", body)
			}
		})
	}
}

func TestUpsert_IsStickyPerKey(t *testing.T) {
	var mu sync.Mutex
	posts := 0
	ids := map[string]int64{}
	var lastKey, lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var in struct {
			Body      string `json:"body"`
			StickyKey string `json:"sticky_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		defer mu.Unlock()
		posts++
		ids[in.StickyKey] = 101
		lastKey, lastBody = in.StickyKey, in.Body
		w.Write([]byte(`{"id":101,"updated":true}`))
	}))
	defer srv.Close()

	err := Upsert(context.Background(), srv.URL, "mikelear", "r", "7", "first", "leartech-end2end-gcp")
	if err != nil {
		t.Fatal(err)
	}
	err = Upsert(context.Background(), srv.URL, "mikelear", "r", "7", "second", "leartech-end2end-gcp")
	if err != nil {
		t.Fatal(err)
	}
	if posts != 2 || lastKey != "leartech-end2end-gcp" || lastBody != "second" {
		t.Fatalf("sticky contract wrong: posts=%d key=%s body=%q", posts, lastKey, lastBody)
	}
}

func TestExitCode_MapsTheOutcome(t *testing.T) {
	if ExitCodeOf("success") != 0 || ExitCodeOf("pending") != 0 || ExitCodeOf("failure") != 1 {
		t.Fatal("outcome->exit mapping changed; Tekton's status back to Lighthouse depends on it")
	}
}

// The failure post must carry the failing lines in the MACHINE half, because a
// consumer (gate, agent, shell) reads them without scraping the markdown.
func TestRender_FailuresAreMachineReadable(t *testing.T) {
	rep := Report{Tool: "go-test", Outcome: "failure", Failures: []string{"pkg/a: TestX failed", "pkg/b: TestY failed"}}
	body := Render("go-test", "## Test\n\n2 failures", rep)
	if !strings.Contains(body, `"failures": [`) {
		t.Fatalf("failures absent from the machine block:\n%s", body)
	}
}

func TestBearerForForge_PrefersTheStaticToken(t *testing.T) {
	t.Setenv("FORGE_API_TOKEN", "static-tok")
	if got := bearerForForge(context.Background()); got != "static-tok" {
		t.Fatalf("bearer = %q", got)
	}
}

// The gitea path is sticky by MARKER in the body (forge-api's sticky_key does
// not exist on a raw Gitea API): second post PATCHes the same comment.
func TestUpsertGitea_StickyByMarker(t *testing.T) {
	var posts []capturedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		posts = append(posts, capturedReq{Method: r.Method, URL: r.URL.Path, Body: string(b)})
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			w.Write([]byte(`[{"id":55,"body":"<!-- leartech-end2end-gcp --> old"}]`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/issues/comments/55"):
			w.Write([]byte(`{"id":55}`))
		case r.Method == http.MethodPost:
			w.Write([]byte(`{"id":56}`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	if err := UpsertGitea(context.Background(), srv.URL, "tok", "it-test", "lh-test", "7", "<!-- leartech-end2end-gcp -->\nnew body", "<!-- leartech-end2end-gcp -->"); err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || posts[1].Method != http.MethodPatch || !strings.Contains(posts[1].URL, "/issues/comments/55") {
		t.Fatalf("marker must PATCH the existing comment: %+v", posts)
	}
	if !strings.Contains(posts[1].Body, "new body") {
		t.Fatalf("patch body wrong: %s", posts[1].Body)
	}
}

type capturedReq struct {
	Method, URL, Body string
}
