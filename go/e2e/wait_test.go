package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The wait's decision table, tested without a cluster. Every case is a failure
// that actually happened, so this file is the regression pack for the readiness
// logic that used to exist in three shell copies and be tested by opening a PR.

// stubHTTP answers per-URL, with a per-URL sequence so a streak can be modelled.
type stubHTTP struct {
	// bodies is the JSON the gate would return.
	bodies map[string]string
	// calls records every URL, so a test can assert what was NOT probed.
	calls []string
	seq   map[string][]int
	// cluster facts, set by withCluster.
	deps []Deployment
	port int
}

func (s *stubHTTP) get(ctx context.Context, url string, timeout time.Duration) (int, error) {
	s.calls = append(s.calls, url)
	if q, ok := s.seq[url]; ok && len(q) > 0 {
		code := q[0]
		s.seq[url] = q[1:]
		if code < 0 {
			return 0, errors.New("connection refused")
		}
		return code, nil
	}
	return 0, errors.New("no such URL in the stub: " + url)
}

func (s *stubHTTP) body(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	if b, ok := s.bodies[url]; ok {
		return []byte(b), nil
	}
	return nil, errors.New("unreachable")
}

func stubOut(t *testing.T, s *stubHTTP) {
	t.Helper()
	oldGet, oldBody, oldDeps, oldPort := httpGet, httpGetBody, depsFor, servicePort
	httpGet, httpGetBody = s.get, s.body
	depsFor = func(ctx context.Context, ns string) ([]Deployment, error) { return s.deps, nil }
	servicePort = func(ctx context.Context, ns, name string) int { return s.port }
	t.Cleanup(func() { httpGet, httpGetBody, depsFor, servicePort = oldGet, oldBody, oldDeps, oldPort })
}

func (s *stubHTTP) withCluster(deps []Deployment, port int) *stubHTTP {
	s.deps, s.port = deps, port
	return s
}

// fixture: a preview whose Service is called preview-leartech-sp-web and whose
// namespace also runs hydra — the shape that made items[0] the wrong pick.
func spWebPreview() []Deployment {
	return []Deployment{
		{Name: "preview-hydra", Ready: 1, Replicas: 1, Image: "oryd/hydra:v25.4.0",
			ReadinessPath: "/health/alive"},
		{Name: "preview-leartech-sp-web", Ready: 1, Replicas: 1,
			Image:         "reg/leartech-sp-web:0.0.0-PR-22-2-SNAPSHOT@sha256:aaa",
			ReadinessPath: "/health", AppName: "leartech-sp-web"},
	}
}

func baseContract(dir string) *Contract {
	c, _ := LoadContract(dir)
	return c
}

// The deployment under test is the declared one, never "whatever sorts first".
// Taking items[0] once picked Hydra's /health/alive for the app's probe, and the
// task then 404'd against the app confidently.
func TestWaitPicksTheDeclaredServiceNotTheFirstDeployment(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{seq: map[string][]int{}}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 200, 200},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if !v.Ready {
		t.Fatalf("not ready: %s\nsteps: %v", v.Reason, v.Steps)
	}
	if !strings.Contains(v.BaseURL, "leartech-sp-web") {
		t.Errorf("base URL = %q; the declared service was not used", v.BaseURL)
	}
	for _, c := range s.calls {
		if strings.Contains(c, "hydra") {
			t.Errorf("probed hydra (%s) — that is the deployment-selection bug", c)
		}
	}
}

// An undeclared service cannot be guessed: the wait says so instead of choosing.
func TestWaitRefusesToGuessTheDeployment(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 100 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if v.Ready {
		t.Fatal("ready with no declared service; picking one of them would test the wrong thing")
	}
	if !strings.Contains(v.Reason, "no service declared") {
		t.Errorf("reason = %q, want it to name the missing declaration", v.Reason)
	}
}

// A declared service that is not in the namespace is a finding, and the reason
// lists what IS there — that is how a wrong chart name is found in seconds.
func TestWaitNamesTheDeploymentsWhenTheServiceIsAbsent(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: not-there\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 100 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if v.Ready || !strings.Contains(v.Reason, "preview-hydra") {
		t.Errorf("reason = %q, want it to list the deployments present", v.Reason)
	}
}

// The chart's readinessProbe is the authoritative path when the contract defers.
func TestWaitHealthPathFromChartProbe(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPathSource: chart\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 200, 200},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if !v.Ready {
		t.Fatalf("not ready: %s", v.Reason)
	}
	if !strings.HasSuffix(v.HealthURL, "/health") {
		t.Errorf("healthURL = %q, want the deployment's /health", v.HealthURL)
	}
}

// A different build in the namespace is the failure that makes every other verdict
// meaningless, so it is checked before the suite runs.
func TestWaitVersionMismatch(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", ExpectedVersion: "deadbeef",
		Timeout: time.Second, PollInterval: time.Millisecond,
	})
	if v.Ready {
		t.Fatal("ready against a deployment carrying a different commit")
	}
	if !strings.Contains(v.Reason, "does not carry this commit") {
		t.Errorf("reason = %q", v.Reason)
	}
}

func TestWaitVersionMatch(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 200, 200},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", ExpectedVersion: "0.0.0-PR-22-2-SNAPSHOT",
		Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if !v.Ready {
		t.Fatalf("not ready: %s", v.Reason)
	}
}

// A repo that declared the gate absent must not be probed for it at all — that
// probe is the 60-180s every run currently pays.
func TestWaitGateAbsentIsNotProbed(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 200, 200},
	}
	stubOut(t, s)

	Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	for _, c := range s.calls {
		if strings.Contains(c, "preview-gate") {
			t.Errorf("probed the gate despite gate: absent (%s)", c)
		}
	}
}

// leartech-auth-ui#111: every deployment ready, the gate holding on a probe URL
// that 404s. The reason must quote the gate's own message.
func TestWaitGateRequiredTimeoutQuotesGateMessage(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: required\n")
	gateURL := "http://preview-gate.jx-pr-22.svc.cluster.local:8090/cgi-bin/gate"
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{gateURL: {-1}}
	s.bodies = map[string]string{gateURL: `{"ready":false,"message":"timeout after 600s: http_probe: probe 'https://hydra-pr111/.well-known/openid-configuration' returned 404"}`}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if v.Ready {
		t.Fatal("ready while the required gate refused")
	}
	if !strings.Contains(v.Reason, "returned 404") {
		t.Errorf("reason does not quote the gate: %q", v.Reason)
	}
}

// A required gate that is not deployed fails fast: falling back would test
// something weaker than the repo asked for.
func TestWaitGateRequiredAbsentFails(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: required\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if v.Ready || !strings.Contains(v.Reason, "preview-gate did not report ready") {
		t.Errorf("reason = %q", v.Reason)
	}
}

// A contract probe that answers the wrong status names the URL and both statuses.
func TestWaitContractProbeWrongStatus(t *testing.T) {
	dir := writeContract(t, `preview:
  healthPath: /health
  service: leartech-sp-web
  gate: absent
  probes:
    - url: http://hydra.${NAMESPACE}.svc:4445/.well-known/openid-configuration
      want: 200
`)
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health":     {200, 200, 200},
		"http://hydra.jx-pr-22.svc:4445/.well-known/openid-configuration": {404},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if v.Ready {
		t.Fatal("ready with a failing contract probe")
	}
	if !strings.Contains(v.Reason, "returned 404, want 200") {
		t.Errorf("reason = %q, want the URL and both statuses", v.Reason)
	}
	if !strings.Contains(v.Reason, "hydra.jx-pr-22.svc") {
		t.Errorf("reason omits the expanded URL: %q", v.Reason)
	}
}

// The ${NAMESPACE} placeholder is expanded against the run's namespace, so one
// declaration works in every preview.
func TestWaitProbeNamespaceExpansion(t *testing.T) {
	dir := writeContract(t, `preview:
  healthPath: /health
  service: leartech-sp-web
  gate: absent
  probes:
    - url: http://hydra.${NAMESPACE}.svc:4445/x
      want: 200
`)
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.other-ns.svc.cluster.local:80/health": {200, 200, 200},
		"http://hydra.other-ns.svc:4445/x":                            {200},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "other-ns", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
	})
	if !v.Ready {
		t.Fatalf("not ready: %s", v.Reason)
	}
}

// A streak that keeps resetting must time out with the last status in the reason,
// which is the difference between "the app is not up" and "the app is up and
// answering something else".
func TestWaitStreakTimeoutNamesTheLastStatus(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 503, 200, 503, 200, 503, 200, 503},
	}
	stubOut(t, s)

	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 20 * time.Millisecond, PollInterval: time.Millisecond,
	})
	if v.Ready {
		t.Fatal("ready on an interrupted streak")
	}
	if !strings.Contains(v.Reason, "HTTP 503") {
		t.Errorf("reason = %q, want the last status", v.Reason)
	}
	if !strings.Contains(v.Reason, "preview.healthPath") {
		t.Errorf("reason does not point at the declaration: %q", v.Reason)
	}
}

// Every step is recorded, so a rehearsal prints the same trace a real run logs.
func TestWaitRecordsSteps(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  service: leartech-sp-web\n  gate: absent\n")
	s := (&stubHTTP{}).withCluster(spWebPreview(), 0)
	s.seq = map[string][]int{
		"http://leartech-sp-web.jx-pr-22.svc.cluster.local:80/health": {200, 200, 200},
	}
	stubOut(t, s)

	var logged []string
	v := Wait(context.Background(), baseContract(dir), WaitOptions{
		Namespace: "jx-pr-22", Timeout: 5 * time.Second, PollInterval: time.Millisecond,
		Log: func(f string, a ...interface{}) { logged = append(logged, fmt.Sprintf(f, a...)) },
	})
	if !v.Ready {
		t.Fatalf("not ready: %s", v.Reason)
	}
	if len(logged) == 0 || len(v.Steps) != len(logged) {
		t.Errorf("steps and log disagree: %d vs %d", len(v.Steps), len(logged))
	}
	joined := strings.Join(v.Steps, "\n")
	for _, want := range []string{"deployment:", "healthPath:", "base:", "gate:", "verdict: READY"} {
		if !strings.Contains(joined, want) {
			t.Errorf("steps missing %q:\n%s", want, joined)
		}
	}
}
