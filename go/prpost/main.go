// prpost is the ONE PR-comment poster for the catalog's tasks.
//
// Eight tasks curl api.github.com directly to upsert a sticky comment — eight
// copies of the same pagination/marker/auth logic, none of it forge-tested,
// none of it able to speak anything but GitHub. This tool replaces those
// blocks: one Go binary, driven by flags, that posts a sticky comment through
// leartech-forge-api (which owns the estate's only forge credential and speaks
// both the GitHub and the Gitea/Forgejo shapes).
//
// DUAL POST, deliberately. Every post writes TWO things:
//
//  1. the VISUAL comment — markdown for a human reading the PR;
//  2. the AI-FRIENDLY block — a fenced ```leartech-report JSON object inside
//     the same comment, machine-parseable by agents, shells and gates.
//
// The machine block carries the same marker-scoped sticky key, so a re-run
// rewrites one comment that serves both readers. A consumer extracts it with a
// one-line match on the fence; it never has to scrape prose.
//
// proven-by: TestRender_PostsVisualAndMachineInOneComment
// proven-by: TestUpsert_IsStickyPerKey
// proven-by: TestExitCode_MapsTheOutcome
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Report is the machine-readable half of every post.
type Report struct {
	Tool      string            `json:"tool"`
	Check     string            `json:"check"` // the check context this post speaks for
	Outcome   string            `json:"outcome"`
	Cluster   string            `json:"cluster,omitempty"`
	Summary   string            `json:"summary"`
	Failures  []string          `json:"failures,omitempty"`
	Metrics   map[string]string `json:"metrics,omitempty"`
	TargetURL string            `json:"target_url,omitempty"`
	Version   string            `json:"version"`
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	tool := flag.String("tool", "", "tool name (e.g. end2end)")
	check := flag.String("check", "", "check context this post speaks for")
	outcome := flag.String("outcome", "", "success | failure | pending")
	cluster := flag.String("cluster", envOr("CLUSTER_ID", ""), "cluster tag")
	summary := flag.String("summary", "", "one-line summary for humans")
	failures := flag.String("failures", "", "comma-separated failure lines")
	targetURL := flag.String("target-url", "", "log/pipeline URL")
	metricsRaw := flag.String("metrics", "", "k=v,k=v")
	file := flag.String("body-file", "", "optional path: markdown body for the visual half")
	api := flag.String("api", envOr("FORGE_API_URL", ""), "leartech-forge-api base URL")
	flag.Parse()

	if *tool == "" || *outcome == "" || *api == "" {
		fmt.Fprintln(os.Stderr, "prpost: -tool, -outcome and FORGE_API_URL/-api are required")
		os.Exit(2)
	}
	rep := Report{
		Tool: *tool, Check: *check, Outcome: *outcome, Cluster: *cluster,
		Summary: *summary, TargetURL: *targetURL, Version: "1",
	}
	for _, f := range strings.Split(*failures, ",") {
		if f = strings.TrimSpace(f); f != "" {
			rep.Failures = append(rep.Failures, f)
		}
	}
	if *metricsRaw != "" {
		rep.Metrics = map[string]string{}
		for _, kv := range strings.Split(*metricsRaw, ",") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				rep.Metrics[k] = v
			}
		}
	}
	visual := *summary
	if *file != "" {
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "prpost:", err)
			os.Exit(2)
		}
		visual = string(b)
	}

	owner := envOr("REPO_OWNER", "")
	name := envOr("REPO_NAME", "")
	pr := envOr("PULL_NUMBER", "")
	if owner == "" || name == "" || pr == "" {
		fmt.Fprintln(os.Stderr, "prpost: REPO_OWNER, REPO_NAME and PULL_NUMBER are required")
		os.Exit(2)
	}

	body := Render(*tool, visual, rep)
	key := StickyKey(*tool, *cluster)
	if err := Upsert(context.Background(), *api, owner, name, pr, body, key); err != nil {
		fmt.Fprintln(os.Stderr, "prpost:", err)
		os.Exit(1)
	}
	os.Exit(ExitCodeOf(*outcome))
}

// StickyKey scopes one comment per tool+cluster, the marker the old curl
// blocks embedded in prose.
func StickyKey(tool, cluster string) string {
	if cluster == "" {
		return "leartech-" + tool
	}
	return "leartech-" + tool + "-" + cluster
}

// Render joins the visual half and the machine block into one comment body.
//
// proven-by: TestRender_PostsVisualAndMachineInOneComment
func Render(tool, visual string, rep Report) string {
	machine, _ := json.MarshalIndent(rep, "", "  ")
	var out bytes.Buffer
	if visual != "" {
		out.WriteString(strings.TrimRight(visual, "\n"))
		out.WriteString("\n\n")
	}
	fmt.Fprintf(&out, "<!-- %s -->\n", StickyKey(tool, rep.Cluster))
	out.WriteString("```leartech-report\n")
	out.Write(bytes.TrimSpace(machine))
	out.WriteString("\n```\n")
	return out.String()
}

// Upsert posts the body through forge-api's sticky-comment route.
//
// proven-by: TestUpsert_IsStickyPerKey
func Upsert(ctx context.Context, api, owner, repo, pr, body, key string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%s/comments", strings.TrimSuffix(api, "/"), owner, repo, pr)
	payload, _ := json.Marshal(map[string]string{"body": body, "sticky_key": key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	tok := bearerForForge(ctx)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("forge-api: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ExitCodeOf maps the report's outcome onto the step's exit status, so the
// Tekton task fails exactly when the check failed — the status back to
// Lighthouse is the task's own exit, not a second channel.
//
// proven-by: TestExitCode_MapsTheOutcome
func ExitCodeOf(outcome string) int {
	switch outcome {
	case "success":
		return 0
	case "failure":
		return 1
	default:
		return 0
	}
}

// bearerForForge returns the bearer for forge-api: a static token when given,
// else a client_credentials mint from LEARTECH_AUTH_* (the workload pattern).
//
// proven-by: TestBearerForForge_PrefersTheStaticToken
func bearerForForge(ctx context.Context) string {
	if tok := os.Getenv("FORGE_API_TOKEN"); tok != "" {
		return tok
	}
	server := strings.TrimSpace(os.Getenv("LEARTECH_AUTH_SERVER_URL"))
	id := strings.TrimSpace(os.Getenv("LEARTECH_AUTH_CLIENT_ID"))
	secret := os.Getenv("LEARTECH_AUTH_CLIENT_SECRET")
	if server == "" || id == "" || secret == "" {
		return ""
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {id},
		"client_secret": {secret},
		"audience":      {"leartech-forge-api"},
		"scope":         {"leartechapi.internal_services"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/oauth2/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	return out.AccessToken
}
