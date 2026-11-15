package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// The three things a wait needs from the outside world. Interfaces rather than
// direct calls so the whole decision table is testable without a cluster — which
// is the entire reason this replaced three shell implementations that could only
// be tested by opening a PR.

// HTTPDoer performs one GET and reports the status code.
type HTTPDoer interface {
	Get(ctx context.Context, url string) (int, error)
}

// Deployment is the subset of a workload a wait needs.
type Deployment struct {
	Name          string
	Ready         int
	Replicas      int
	Image         string
	ReadinessPath string
	AppName       string
}

// Cluster lists the workloads in a preview namespace.
type Cluster interface {
	Deployments(ctx context.Context, namespace string) ([]Deployment, error)
}

// WaitOptions is what the caller knows that the declaration does not.
type WaitOptions struct {
	Namespace       string
	ExpectedVersion string
	Mode            string // "web" or "ui"
	Timeout         time.Duration
	DetectTimeout   time.Duration
	PollInterval    time.Duration
	// Log receives one line per step, so a rehearsal and a real wait print the
	// same thing.
	Log func(format string, a ...interface{})
}

// Verdict is the outcome plus the reason, and the reason is the product: an
// end2end failure that does not name a deployment, a URL and a status sends the
// reader to the application.
type Verdict struct {
	Ready     bool
	Reason    string
	Steps     []string
	BaseURL   string
	HealthURL string
	GateUsed  bool
	Detail    string
}

// Wait runs the readiness decision once.
func Wait(ctx context.Context, c *Contract, opts WaitOptions) Verdict {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 3 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Minute
	}
	if opts.DetectTimeout <= 0 {
		opts.DetectTimeout = 15 * time.Second
	}
	logf := opts.Log
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	v := &Verdict{}
	step := func(format string, a ...interface{}) {
		line := fmt.Sprintf(format, a...)
		v.Steps = append(v.Steps, line)
		logf("%s", line)
	}

	deps, depErr := depsFor(ctx, opts.Namespace)
	_ = depErr

	// ── which workload is under test ───────────────────────────────────────
	dep, depReason := pickDeployment(deps, c.Service)
	if dep == nil {
		step("deployments: %s", depReason)
	} else {
		step("deployment: %s  %d/%d ready  image %s", dep.Name, dep.Ready, dep.Replicas, shortImage(dep.Image))
	}

	// ── the health path ────────────────────────────────────────────────────
	healthPath := c.HealthPath
	healthFrom := c.HealthFrom
	if healthPath == "" {
		if dep != nil && dep.ReadinessPath != "" {
			healthPath, healthFrom = dep.ReadinessPath, "chart readinessProbe"
		} else {
			healthPath, healthFrom = DefaultHealthPath, "default"
		}
	}
	step("healthPath: %s   [%s]", healthPath, healthFrom)

	// ── the base URL ───────────────────────────────────────────────────────
	baseURL, baseWhy := pickBaseURL(c, dep, opts)
	if dep == nil {
		baseWhy = depReason + ". " + baseWhy
	}
	if baseURL == "" {
		// No target is not "wait a bit longer", it is a declaration or deployment
		// problem, and saying so is the whole point of naming the deployments above.
		v.Reason = baseWhy
		step("base: none — stopping before probing")
		return *v
	}
	v.BaseURL = baseURL
	v.HealthURL = strings.TrimSuffix(baseURL, "/") + healthPath
	step("base: %s   (%s)", baseURL, baseWhy)

	// ── version ────────────────────────────────────────────────────────────
	if c.VersionAnchor && opts.ExpectedVersion != "" {
		switch {
		case dep == nil:
			step("version: cannot anchor — no deployment to read")
		case !strings.Contains(dep.Image, opts.ExpectedVersion):
			v.Reason = fmt.Sprintf(
				"the deployment does not carry this commit: %s runs %s, expected a tag "+
					"containing %s. The preview is a DIFFERENT build; anything the suite "+
					"concludes would be about that one.",
				dep.Name, shortImage(dep.Image), opts.ExpectedVersion)
			step("version: MISMATCH")
			return *v
		default:
			step("version: %s matches", opts.ExpectedVersion)
		}
	}

	// ── the gate ───────────────────────────────────────────────────────────
	gateURL := fmt.Sprintf("http://preview-gate.%s.svc.cluster.local:8090/cgi-bin/gate", opts.Namespace)
	switch c.Gate {
	case GateAbsent:
		step("gate: declared absent, not probed")
	case GateRequired:
		v.GateUsed = true
		step("gate: required at %s", gateURL)
		if reason := waitForGate(ctx, gateURL, opts, step); reason != "" {
			v.Reason = reason
			return *v
		}
	default:
		// auto: one probe, not a 60-180s window. The declaration is the answer to
		// "is there a gate"; the probe only confirms it is up yet.
		v.GateUsed = probeOnce(ctx, gateURL, opts.DetectTimeout)
		step("gate: auto, %s", map[bool]string{true: "found", false: "not deployed"}[v.GateUsed])
		if v.GateUsed {
			if reason := waitForGate(ctx, gateURL, opts, step); reason != "" {
				v.Reason = reason
				return *v
			}
		}
	}

	// ── readiness ──────────────────────────────────────────────────────────
	if !v.GateUsed {
		if reason := waitForStreak(ctx, v.HealthURL, opts, step); reason != "" {
			v.Reason = reason
			return *v
		}
	}

	// ── the contract's own probes ──────────────────────────────────────────
	for _, p := range c.Probes {
		url := expandNamespace(p.URL, opts.Namespace)
		code, err := httpGet(ctx, url, opts.PollInterval)
		if err != nil {
			v.Reason = fmt.Sprintf("probe %s: %v", url, err)
			step("probe: FAIL %s (%v)", url, err)
			return *v
		}
		if code != p.Want {
			v.Reason = fmt.Sprintf(
				"probe %s returned %d, want %d%s. This is the shape of leartech-auth-ui#111: "+
					"every deployment ready, and the gate held the preview closed on a probe "+
					"URL that 404s.", url, code, p.Want, noteSuffix(p.Note))
			step("probe: FAIL %s -> %d", url, code)
			return *v
		}
		step("probe: ok %s -> %d", url, code)
	}

	v.Ready = true
	step("verdict: READY")
	return *v
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}

// pickDeployment finds the workload under test, not "whatever sorts first".
func pickDeployment(deps []Deployment, service string) (*Deployment, string) {
	if len(deps) == 0 {
		return nil, "none found in the namespace"
	}
	if service != "" {
		for i := range deps {
			if deps[i].Name == service || strings.HasSuffix(deps[i].Name, "-"+service) {
				return &deps[i], ""
			}
		}
		for i := range deps {
			if deps[i].AppName == service {
				return &deps[i], ""
			}
		}
	}
	if service != "" {
		return nil, fmt.Sprintf(
			"none matches the declared service %q (saw %s). A preview also runs its own "+
				"infrastructure — picking one of those would test Hydra's readiness and call "+
				"it the app's.", service, deploymentNames(deps))
	}
	return nil, fmt.Sprintf(
		"no service declared, so the deployment cannot be chosen (saw %s). Declare "+
			"preview.service: the first deployment is usually the preview's infrastructure.",
		deploymentNames(deps))
}

func deploymentNames(deps []Deployment) string {
	names := make([]string, 0, len(deps))
	for _, d := range deps {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func pickBaseURL(c *Contract, dep *Deployment, opts WaitOptions) (string, string) {
	if dep != nil {
		port := c.Port
		if port == 0 {
			port = 80
		}
		svc := c.Service
		if svc == "" {
			svc = dep.Name
		}
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", svc, opts.Namespace, port),
			"the app's Service, not the ingress"
	}
	return "", "no deployment under test, so there is no Service to probe. " +
		"Declare preview.service in " + ContractFile + "."
}

// probeOnce reports whether a URL answers at all within budget.
func probeOnce(ctx context.Context, url string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if code, err := httpGet(ctx, url, 3*time.Second); err == nil && (code == 200 || code == 503) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// waitForGate polls the gate and, on timeout, quotes the gate's own message —
// which is already the best diagnosis in the system.
func waitForGate(ctx context.Context, gateURL string, opts WaitOptions, step func(string, ...interface{})) string {
	deadline := time.Now().Add(opts.Timeout)
	for {
		code, err := httpGet(ctx, gateURL, 5*time.Second)
		if err == nil && code == 200 {
			step("gate: READY")
			return ""
		}
		if time.Now().After(deadline) {
			detail := gateDetail(ctx, gateURL)
			step("gate: TIMEOUT")
			return fmt.Sprintf(
				"preview-gate did not report ready within %s. Gate detail: %s",
				opts.Timeout.Truncate(time.Second), detail)
		}
		select {
		case <-ctx.Done():
			return "cancelled while waiting for the gate"
		case <-time.After(opts.PollInterval):
		}
	}
}

func gateDetail(ctx context.Context, gateURL string) string {
	body, err := httpGetBody(ctx, gateURL, 5*time.Second)
	if err != nil || len(body) == 0 {
		return "(gate unreachable)"
	}
	var d struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &d); err == nil && d.Message != "" {
		return d.Message
	}
	compact := strings.Join(strings.Fields(string(body)), " ")
	if len(compact) > 300 {
		compact = compact[:300] + "…"
	}
	return compact
}

// waitForStreak waits for three consecutive 2xx, and on timeout names the URL, the
// last status and the deployment it was probing.
func waitForStreak(ctx context.Context, url string, opts WaitOptions, step func(string, ...interface{})) string {
	const required = 3
	streak, lastCode, lastErr := 0, 0, error(nil)
	deadline := time.Now().Add(opts.Timeout)
	for streak < required {
		code, err := httpGet(ctx, url, 5*time.Second)
		lastErr = err
		if code != 0 {
			lastCode = code
		}
		if err == nil && code >= 200 && code < 300 {
			streak++
			step("probe: %d/%d ok", streak, required)
		} else {
			streak = 0
		}
		if streak < required {
			if time.Now().After(deadline) {
				last := lastErr.Error()
				if lastCode != 0 {
					// A status is the more useful fact when we have one: it separates
					// "not up" from "up and answering something else".
					last = fmt.Sprintf("HTTP %d", lastCode)
				}
				return fmt.Sprintf(
					"%s never returned %d consecutive 2xx within %s (last: %s). "+
						"If the app serves a different path, set preview.healthPath in %s — "+
						"the probe no longer guesses between candidates.",
					url, required, opts.Timeout.Truncate(time.Second), last, ContractFile)
			}
			select {
			case <-ctx.Done():
				return "cancelled while waiting for the preview"
			case <-time.After(opts.PollInterval):
			}
		}
	}
	return ""
}

func shortImage(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		image = image[i+1:]
	}
	if len(image) > 60 {
		image = image[:60] + "…"
	}
	return image
}

func expandNamespace(url, ns string) string {
	return strings.ReplaceAll(url, "${NAMESPACE}", ns)
}

// ── the default wiring ─────────────────────────────────────────────────────

// depsFor is a var so a test can describe a namespace without a cluster.
var depsFor = realDeployments

// servicePort resolves a Service's port; overridden in tests.
var servicePort = portOf

// httpGet is the one place a GET happens, so the tests can replace the behaviour
// by swapping this function rather than standing up a server per case.
var httpGet = func(ctx context.Context, url string, timeout time.Duration) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}

var httpGetBody = func(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if readErr != nil || len(buf) > 1<<20 {
			break
		}
	}
	return buf, nil
}

// runWait is the CLI. Rehearse is the same code with the log switched on and a
// shorter budget, so what a developer sees is what CI runs.
func runWait(args []string, rehearse bool) int {
	fs := flag.NewFlagSet("e2e wait", flag.ContinueOnError)
	dir := fs.String("dir", ".", "repository root")
	ns := fs.String("namespace", os.Getenv("PREVIEW_NAMESPACE"), "preview namespace")
	version := fs.String("version", os.Getenv("PULL_PULL_SHA"), "commit the preview must carry")
	mode := fs.String("mode", "web", "web or ui")
	timeout := fs.Duration("timeout", 10*time.Minute, "readiness timeout")
	if err := fs.Parse(args); err != nil {
		return 64
	}
	if *ns == "" {
		fmt.Fprintln(os.Stderr, "e2e wait: -namespace is required (or set $PREVIEW_NAMESPACE)")
		return 64
	}
	c, findings := LoadContract(*dir)
	if c == nil {
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, f)
		}
		return 2
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", f)
	}
	opts := WaitOptions{
		Namespace:       *ns,
		ExpectedVersion: *version,
		Mode:            *mode,
		Timeout:         *timeout,
		DetectTimeout:   15 * time.Second,
		PollInterval:    3 * time.Second,
		Log:             func(format string, a ...interface{}) { fmt.Printf(format+"\n", a...) },
	}
	v := Wait(context.Background(), c, opts)
	if v.Ready {
		fmt.Printf("==> preview READY in %d step(s)\n", len(v.Steps))
		return 0
	}
	fmt.Fprintf(os.Stderr, "==> preview NOT ready\n%s\n", v.Reason)
	return 1
}
