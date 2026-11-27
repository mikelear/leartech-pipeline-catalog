package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// render parses a manifest stream the way the tool does, so every test states its chart as the render
// it produces rather than as a fixture the loader might handle differently.
func render(t *testing.T, body string) []obj {
	t.Helper()
	objs, err := load("test", strings.NewReader(body))
	if err != nil {
		t.Fatalf("loading the fixture render: %v", err)
	}
	return objs
}

// a consumer whose baseURL and Service agree — the control every rule below is a change from.
const cleanConsumer = `apiVersion: v1
kind: Service
metadata:
  name: app
  namespace: ns
spec:
  ports:
  - port: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
  namespace: ns
spec:
  template:
    spec:
      containers:
      - name: app
        env:
        - name: LEARTECH_AUTH_AUDIENCE
          value: leartech-app-service
---
apiVersion: maestro.leartech.com/v1alpha1
kind: EventRegistration
metadata:
  name: app
  namespace: ns
spec:
  baseURL: http://app.ns.svc.cluster.local:8080
  authentication:
    strategy: internal
    audience: leartech-app-service
  consumes:
  - name: thing.happened
    endpoint: /api/v1/event/thing.happened
`

// The control: a chart wired correctly produces no findings at all, so no rule below can be passing
// merely because everything passes.
func TestCleanRenderHasNoFindings(t *testing.T) {
	if got := checkEventRegistrations(render(t, cleanConsumer), nil); len(got) != 0 {
		t.Fatalf("a clean consumer produced %d finding(s): %v", len(got), got)
	}
}

// failure 3, the live one: the CR's baseURL names a Service the chart does not render, because
// fullnameOverride rewrote the Service name. Maestro dials a name that does not resolve, and the only
// reader is Maestro at dispatch time.
//
// proven-by: TestM1_BaseURLNamesNoRenderedService
func TestM1_BaseURLNamesNoRenderedService(t *testing.T) {
	body := strings.Replace(cleanConsumer,
		"  name: app\n  namespace: ns\nspec:\n  ports:",
		"  name: umbrella-app\n  namespace: ns\nspec:\n  ports:", 1)

	got := ruleOf(checkEventRegistrations(render(t, body), nil), "M1")
	if len(got) != 1 {
		t.Fatalf("want exactly one M1 finding, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0].Detail, "umbrella-app") {
		t.Errorf("the finding does not name the Service the render actually emits: %s", got[0])
	}
}

// A baseURL that is not a URL at all: measured live as `http://auth-service:8080` — no namespace, no
// .svc — which no render produces either, but as a parse failure it gets a different sentence.
//
// proven-by: TestM1_BaseURLThatIsNotAURL
func TestM1_BaseURLThatIsNotAURL(t *testing.T) {
	body := strings.Replace(cleanConsumer,
		"  baseURL: http://app.ns.svc.cluster.local:8080", "  baseURL: app:8080", 1)

	got := ruleOf(checkEventRegistrations(render(t, body), nil), "M1")
	if len(got) != 1 || !strings.Contains(got[0].Detail, "not http(s)://") {
		t.Fatalf("want one M1 finding about the URL shape, got %v", got)
	}
}

// A consumer with no baseURL at all — the `| default`-nothing break.
func TestM1_ConsumerWithNoBaseURL(t *testing.T) {
	body := strings.Replace(cleanConsumer,
		"  baseURL: http://app.ns.svc.cluster.local:8080\n", "", 1)

	got := ruleOf(checkEventRegistrations(render(t, body), nil), "M1")
	if len(got) != 1 || !strings.Contains(got[0].Detail, "no spec.baseURL") {
		t.Fatalf("want one M1 finding about the missing baseURL, got %v", got)
	}
}

// failure 4: the CR's audience and the Deployment's own LEARTECH_AUTH_AUDIENCE disagree, so Maestro
// mints one audience and the service verifies the other. Both are set in the same values file.
//
// proven-by: TestMA1_ConsumerAudienceDiffersFromTheDeployments
func TestMA1_ConsumerAudienceDiffersFromTheDeployments(t *testing.T) {
	body := strings.Replace(cleanConsumer,
		"    audience: leartech-app-service", "    audience: leartech-other-service", 1)

	got := ruleOf(checkEventRegistrations(render(t, body), nil), "MA1")
	if len(got) != 1 {
		t.Fatalf("want one MA1 finding, got %v", got)
	}
	for _, want := range []string{"leartech-other-service", "leartech-app-service"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("the finding does not name both audiences (%s): %s", want, got[0])
		}
	}
}

// MA2: a consumer that enforces its own audience but pins none in the CR gets a dispatch token carrying
// the plain service audience — which works only because it accepts any audience.
//
// proven-by: TestMA2_EnforcingConsumerThatPinsNoAudience
func TestMA2_EnforcingConsumerThatPinsNoAudience(t *testing.T) {
	body := strings.Replace(cleanConsumer, "    audience: leartech-app-service\n", "", 1)

	got := ruleOf(checkEventRegistrations(render(t, body), nil), "MA2")
	if len(got) != 1 {
		t.Fatalf("want one MA2 finding, got %v", got)
	}
	if strings.Contains(got[0].Detail, "leartech-app-service\n") {
		t.Errorf("MA2 leaked into MA1: %s", got[0])
	}
}

// failure 1 was literally the empty annotationKeys list: auth-service announced three annotations
// against a registration that declared none, and Maestro 400'd the announce.
//
// proven-by: TestMA3_ProducerWithNoAnnotationKeys
func TestMA3_ProducerWithNoAnnotationKeys(t *testing.T) {
	const producer = `apiVersion: maestro.leartech.com/v1alpha1
kind: EventRegistration
metadata:
  name: pr-events
  namespace: ns
spec:
  baseURL: http://pr-events.ns.svc.cluster.local:8080
  authentication:
    strategy: internal
  produces:
  - name: pr.merged
`
	got := ruleOf(checkEventRegistrations(render(t, producer), nil), "MA3")
	if len(got) != 1 {
		t.Fatalf("want one MA3 finding for a producer with no annotationKeys, got %v", got)
	}
	if !strings.Contains(got[0].Detail, "annotation keys []") {
		t.Errorf("the finding does not quote the failure Maestro reports: %s", got[0])
	}
}

// A producer that DOES declare keys passes, and an event it does not declare is not this rule's
// business — the superset half needs the producer's own manifest, which is a repo test.
func TestMA3_ProducerWithKeysPasses(t *testing.T) {
	const producer = `apiVersion: maestro.leartech.com/v1alpha1
kind: EventRegistration
metadata:
  name: pr-events
  namespace: ns
spec:
  authentication:
    strategy: internal
  produces:
  - name: pr.merged
    annotationKeys: [repo, pr]
`
	if got := ruleOf(checkEventRegistrations(render(t, producer), nil), "MA3"); len(got) != 0 {
		t.Fatalf("a producer declaring its keys produced %v", got)
	}
}

// failure 2, checked as declaration against declaration: the audience the consumer pins must be one
// maestro-internal-services is allowed to mint, or the mint is refused.
//
// proven-by: TestMA4_AudienceMissingFromTheAuthgraph
func TestMA4_AudienceMissingFromTheAuthgraph(t *testing.T) {
	allowed := map[string]bool{"leartech-notification-service": true}
	got := ruleOf(checkEventRegistrations(render(t, cleanConsumer), allowed), "MA4")
	if len(got) != 1 {
		t.Fatalf("want one MA4 finding for an unmintable audience, got %v", got)
	}
	if !strings.Contains(got[0].Detail, "not been whitelisted") {
		t.Errorf("the finding does not quote Hydra's refusal: %s", got[0])
	}
}

// The same render against an authgraph that DOES carry the edge passes, so MA4 is a comparison and not
// a blanket objection.
func TestMA4_AudienceInTheAuthgraphPasses(t *testing.T) {
	allowed := map[string]bool{"leartech-app-service": true}
	if got := ruleOf(checkEventRegistrations(render(t, cleanConsumer), allowed), "MA4"); len(got) != 0 {
		t.Fatalf("a mintable audience produced %v", got)
	}
}

// Two CRs on this cluster set no strategy at all, and the CRD accepts only "internal". A typo and an
// omission fail the same way at the same time.
//
// proven-by: TestM4_StrategyIsMissingOrUnknown
func TestM4_StrategyIsMissingOrUnknown(t *testing.T) {
	for name, body := range map[string]string{
		"missing": strings.Replace(cleanConsumer, "    strategy: internal\n", "", 1),
		"typo":    strings.Replace(cleanConsumer, "strategy: internal", "strategy: InternaI", 1),
	} {
		if got := ruleOf(checkEventRegistrations(render(t, body), nil), "M4"); len(got) != 1 {
			t.Errorf("%s strategy: want one M4 finding, got %v", name, got)
		}
	}
}

// An endpoint with no leading slash is appended to baseURL into a 404, and a consume entry with no name
// registers and can never be dispatched to.
//
// proven-by: TestM2_EndpointAndNameShape
func TestM2_EndpointAndNameShape(t *testing.T) {
	body := strings.Replace(cleanConsumer, "    endpoint: /api/v1/event/thing.happened",
		"    endpoint: api/v1/event/thing.happened", 1)
	if got := ruleOf(checkEventRegistrations(render(t, body), nil), "M2"); len(got) != 1 {
		t.Fatalf("want one M2 finding for an endpoint without a slash, got %v", got)
	}

	body = strings.Replace(cleanConsumer, "  - name: thing.happened\n", "  - name: \"\"\n", 1)
	if got := ruleOf(checkEventRegistrations(render(t, body), nil), "M2"); len(got) != 1 {
		t.Fatalf("want one M2 finding for a nameless consume entry, got %v", got)
	}
}

// A render with no EventRegistration is a failure, not a pass: this tool's whole question is Maestro
// wiring, so "nothing to check" means the caller pointed it at the wrong render.
//
// proven-by: TestM0_NoEventRegistrationInTheRenderIsAFailure
func TestM0_NoEventRegistrationInTheRenderIsAFailure(t *testing.T) {
	const noReg = `apiVersion: v1
kind: Service
metadata:
  name: app
`
	got := checkEventRegistrations(render(t, noReg), nil)
	if len(got) != 1 || got[0].Rule != "M0" {
		t.Fatalf("want exactly one M0 finding, got %v", got)
	}
}

// A chart that renders nothing is an error: it means the render command is wrong, and reporting "no
// findings" over an empty stream is the vacuous pass this whole family of tools exists to refuse.
func TestLoad_EmptyRenderIsAnError(t *testing.T) {
	if _, err := load("test", strings.NewReader("\n---\n\n")); err == nil {
		t.Fatal("load accepted a render with no objects")
	}
}

// A valueFrom ref cannot be judged here: this tier compares two declarations in the same render, and a
// cluster read is the live tier's business. The rule must not fire on a ref it cannot read.
func TestMA1_ValueFromAudienceIsNotCompared(t *testing.T) {
	body := strings.Replace(cleanConsumer,
		`        - name: LEARTECH_AUTH_AUDIENCE
          value: leartech-app-service`,
		`        - name: LEARTECH_AUTH_AUDIENCE
          valueFrom:
            secretKeyRef:
              name: s
              key: k`, 1)
	if got := checkEventRegistrations(render(t, body), nil); len(got) != 0 {
		t.Fatalf("a valueFrom audience produced findings it cannot justify: %v", got)
	}
}

// MA4 cannot be evaluated without the edge list, and a rule that cannot be evaluated must say so rather
// than report a pass.
//
// proven-by: TestLoadAudiences_MissingFileIsAnError
func TestLoadAudiences_MissingFileIsAnError(t *testing.T) {
	dir := t.TempDir()

	if _, err := loadAudiences(filepath.Join(dir, "absent.txt")); err == nil {
		t.Fatal("loadAudiences accepted a missing file")
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("# nothing but comments\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAudiences(empty); err == nil {
		t.Fatal("loadAudiences accepted a file listing no audiences")
	}

	good := filepath.Join(dir, "good.txt")
	if err := os.WriteFile(good, []byte("# edges\nleartech-ba-service\n\nleartech-subscription-service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadAudiences(good)
	if err != nil {
		t.Fatalf("loadAudiences: %v", err)
	}
	if !got["leartech-ba-service"] || !got["leartech-subscription-service"] || len(got) != 2 {
		t.Errorf("loadAudiences = %v", got)
	}
}

// ruleOf filters a finding set to one rule, so each test states the rule it is about and cannot pass on
// another rule's objection.
func ruleOf(findings []Finding, rule string) []Finding {
	var out []Finding
	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}
