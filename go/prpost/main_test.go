package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// The machine half must carry the same detail as the human half. An agent reading
// `outcome: failure, summary: 1/4 checks passed` has no way to learn WHICH check failed
// unless the tests are in the block too — it would have to parse the markdown beside it,
// which is the coupling the two-payload design exists to avoid.
func TestRender_MachineBlockCarriesTheTests(t *testing.T) {
	tests := []Test{
		{Name: "smoke", Status: "pass", Duration: "503ms", Message: "OK"},
		{Name: "audience", Status: "fail", Duration: "765ms", Message: "BINDING BROKEN"},
	}
	rep := Report{Tool: "end2end", Check: "end2end", Outcome: "failure", Cluster: "gcp",
		Summary: "1/2 checks passed", Tests: tests, Version: "1"}
	body := Render("end2end", "whatever", rep)

	open := strings.Index(body, "```leartech-report\n")
	close_ := strings.Index(body[open:], "\n```")
	var back Report
	if err := json.Unmarshal([]byte(body[open+len("```leartech-report\n"):open+close_]), &back); err != nil {
		t.Fatalf("machine block does not decode: %v", err)
	}
	if len(back.Tests) != 2 {
		t.Fatalf("machine block has %d test(s), want 2 — an agent cannot see which check failed", len(back.Tests))
	}
	if back.Tests[1].Name != "audience" || back.Tests[1].Status != "fail" {
		t.Errorf("machine block lost the failing row: %+v", back.Tests[1])
	}
}

// A cell with a pipe or a newline breaks a markdown table into nonsense rows. The estate's
// real messages contain both (a jq filter, a multi-line failure tail).
func TestOneLineFlattensACell(t *testing.T) {
	cases := map[string]string{
		"a | b":                  `a \| b`,
		"line one\nline two":     "line one line two",
		"  spaced   out  ":       "spaced out",
		strings.Repeat("x", 400): strings.Repeat("x", 300) + "…",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A fact the step did not supply renders a placeholder, never nothing. An empty string in a
// sentence reads as a formatting glitch; a "?" reads as a missing input.
func TestMissingFactsRenderPlaceholders(t *testing.T) {
	out := RenderVisual("coverage", Report{Outcome: "success", Cluster: "gcp"}, Facts{}, nil, "")
	for _, want := range []string{"Coverage: ?", "threshold ?", "unavailable — reason not reported"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing fact did not render %q:\n%s", want, out)
		}
	}
}

// A percentage that came from an empty profile is not a measurement, and the headline must
// not print it as one.
func TestCoverageHeadlineSaysNotMeasured(t *testing.T) {
	out := RenderVisual("coverage", Report{Outcome: "failure", Cluster: "gcp"},
		Facts{"coverage": "0.0%", "threshold": "60.0%", "measured": "nothing"}, nil, "")
	if !strings.Contains(out, "NOT MEASURED") {
		t.Errorf("an unmeasured run should not print a percentage:\n%s", out)
	}
	if strings.Contains(out, "Coverage: 0.0%") {
		t.Errorf("the headline still shows 0.0%%:\n%s", out)
	}
}

// An unknown tool must still post something truthful. A new step gets a plain comment before
// it gets a designed one, and the golden suite makes "no renderer yet" visible.
func TestUnknownToolFallsBack(t *testing.T) {
	// Tool set on the Report as well as passed as the dispatch key: that is what a caller has,
	// and the renderer reads the Report.
	out := RenderVisual("brand-new-check", Report{Tool: "brand-new-check", Outcome: "failure",
		Cluster: "az", Summary: "3 of 5 sub-checks failed", Failures: []string{"a: bad"}},
		Facts{}, nil, "")
	for _, want := range []string{":x:", "brand-new-check", "3 of 5", "a: bad"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback omitted %q:\n%s", want, out)
		}
	}
}

// A facts FILE with a multi-line value: `lowest` is a markdown table, transported as one line
// with a two-character \n. Without the unescape every table row would carry a literal
// backslash-n, which looks like a rendering bug and is a transport one.
func TestReadFactsUnescapesNewlines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "facts")
	body := "coverage=82.3%\nthreshold=60.0%\n" +
		`lowest=| a.go | Do | 41.2% |\n| b.go | Publish | 58.0% |` + "\n" +
		"delta_reason=clone failed, could not read Username\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	f := readFacts("", path)
	if got := f["lowest"]; strings.Count(got, "\n") != 1 {
		t.Errorf("lowest has %d newline(s), want 1 — the rows must be real line breaks:\n%q",
			strings.Count(got, "\n"), got)
	}
	if strings.Contains(f["lowest"], `\n`) {
		t.Errorf("lowest still carries an escape: %q", f["lowest"])
	}
	// A comma is data, not a separator. This value is a real git error.
	if !strings.Contains(f["delta_reason"], "clone failed, could not read Username") {
		t.Errorf("delta_reason lost its comma or its text: %q", f["delta_reason"])
	}
	if f["coverage"] != "82.3%" {
		t.Errorf("coverage = %q", f["coverage"])
	}
}

// The file wins over the inline flag, so a caller migrating from -facts to -facts-file cannot
// accidentally get half of each.
func TestReadFactsFileWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "facts")
	if err := os.WriteFile(path, []byte("coverage=99.9%\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := readFacts("coverage=11.1%", path)
	if f["coverage"] != "99.9%" {
		t.Errorf("coverage = %q, want the file's 99.9%%", f["coverage"])
	}
}

// The double-wrap defect, measured on console-api#25.
//
// end2end-report renders with -render (human + machine block) and then hands that string to
// prpost again to post it. Before this guard the second call appended a block built from its
// own defaults, so a FAILED run's comment ended:
//
//	"outcome": "success", "summary": "end2end update"
//
// — and a reader that takes the last block learns the opposite of what happened.
//
// proven-by: TestRender_DoesNotDoubleWrapARenderedBody
func TestRender_DoesNotDoubleWrapARenderedBody(t *testing.T) {
	failed := Report{Tool: "end2end", Check: "end2end", Outcome: "failure",
		Cluster: "gcp", Summary: "2/4 checks passed, 1 failed"}
	first := Render("end2end", "### :x: End-to-end: FAIL `[gcp]`", failed)

	// The step posts the rendered string again, with no flags of its own.
	second := Render("end2end", first, Report{Tool: "end2end", Check: "end2end",
		Outcome: "success", Cluster: "gcp", Summary: "end2end update"})

	if n := strings.Count(second, "```leartech-report"); n != 1 {
		t.Fatalf("want exactly 1 machine block, got %d\n---\n%s", n, second)
	}
	if !strings.Contains(second, `"outcome": "failure"`) {
		t.Errorf("the surviving block must carry the run's real outcome:\n%s", second)
	}
	if strings.Contains(second, "end2end update") {
		t.Errorf("a defaulted summary must not reach the comment:\n%s", second)
	}
}

// The suite emits folded, indented messages; the table flattened them and the machine block
// did not, so one comment described the same string two ways.
//
// proven-by: TestReadTests_FlattensFoldedMessages
func TestReadTests_FlattensFoldedMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tests.json")
	// \n inside the JSON string is what the suite emits: a real newline once decoded.
	body := `[{"name":"fleet-test","status":"skip",` +
		`"message":"SKIP: STAGING_URL unset. This check is       staging-only: it drives\n` +
		`the real login flow."}]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := readTests(path)
	if len(got) != 1 {
		t.Fatalf("want 1 test, got %d", len(got))
	}
	want := "SKIP: STAGING_URL unset. This check is staging-only: it drives the real login flow."
	if got[0].Message != want {
		t.Errorf("message = %q, want %q", got[0].Message, want)
	}
}

// forge-api serves more than one forge, so the poster has to say which one it means. The host is
// taken from REPO_URL (the clone URL Lighthouse injects) rather than a new setting: a pipeline
// already knows where its repo lives.
//
// proven-by: TestForgeHost_ReadsTheHostFromTheRepoURL
func TestForgeHost_ReadsTheHostFromTheRepoURL(t *testing.T) {
	t.Setenv("REPO_URL", "https://forgejo.jx.leartech.com/it-test/ship-proven-go-service.git")
	if got := forgeHost(); got != "forgejo.jx.leartech.com" {
		t.Errorf("forgeHost = %q, want the repo host", got)
	}

	t.Setenv("REPO_URL", "https://github.com/mikelear/leartech-gate.git")
	if got := forgeHost(); got != "github.com" {
		t.Errorf("forgeHost = %q, want github.com", got)
	}

	// Absent or unparseable leaves forge-api on its default forge: the behaviour every
	// existing caller has, and the reason this is additive.
	for _, raw := range []string{"", "not a url"} {
		t.Setenv("REPO_URL", raw)
		if got := forgeHost(); got != "" {
			t.Errorf("REPO_URL=%q: forgeHost = %q, want empty", raw, got)
		}
	}
}
