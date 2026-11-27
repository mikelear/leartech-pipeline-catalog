// Command maestrostd checks a chart's Maestro wiring, the way chartstd checks its charts.
//
// A chart that registers with Maestro — as a producer (announces events) or a consumer (receives
// dispatched events) — renders an EventRegistration CR plus the service's own auth env. Nothing checked
// that the two agree, so four separate wiring mistakes each passed every gate and failed at dispatch
// time, live, one after another:
//
//	failure 1  a producer declared an event with NO annotationKeys and announced three
//	           "event is registered with annotation keys [], but found [email tenant user_id]"
//	failure 2  a consumer's audience was not on the minting client's Hydra allow-list
//	           "Requested audience 'leartech-subscription-service' has not been whitelisted"
//	failure 3  the CR's baseURL named no Service the chart renders
//	           "dial tcp: lookup leartech-subscription-api.ship-proven.svc.cluster.local: no such host"
//	failure 4  a consumer's audience differed from its own LEARTECH_AUTH_AUDIENCE, so every
//	           dispatch 401'd
//
// THE RULES, and the failure each exists for:
//
//	M0   the render contains at least one EventRegistration
//	     a gate over an empty set is a vacuous pass, which is how "we check Maestro" can be true
//	     and mean nothing.
//	M1   spec.baseURL parses, and its hostnames a Service the SAME render emits
//	     failure 3. A free-text baseURL beside a chart-derived Service name drifts silently, and
//	     the only reader is Maestro at dispatch time.
//	M2   every consumes[].endpoint starts with "/", and no name is empty
//	     an endpoint without a leading slash is appended to baseURL into a 404 nobody sees, and a
//	     consumer with a blank name registers but can never be dispatched to.
//	M3   every produces[].name is non-empty
//	     an entry that names no event is dead weight that reads as coverage.
//	M4   authentication.strategy is a value Maestro accepts (internal), when there is a consumer
//	     a typo'd strategy registers and then every dispatch fails one way. Measured live: two CRs
//	     on this cluster set none.
//	MA1  authentication.audience equals the Deployment's LEARTECH_AUTH_AUDIENCE
//	     failure 4. Both are set in the same values file, independently, and a mismatch means
//	     Maestro mints a token the service's own verifier rejects.
//	MA2  a consumer that sets LEARTECH_AUTH_AUDIENCE also pins authentication.audience
//	     without it the dispatch token carries the service's plain audience, which works only
//	     because some consumers accept any audience — the weakness, not the standard.
//	MA3  a producer's annotationKeys is non-empty
//	     failure 1 was literally the empty case. Whether the keys are the RIGHT keys needs the
//	     producer's own manifest; that is a repo test, and this rule is the floor.
//	MA4  every distinct consumer audience is a declared maestro-internal-services edge
//	     failure 2, checked as a declaration against a declaration: the authgraph in
//	     leartech-auth-service is the estate's single source of truth for what that client may
//	     mint, and this reads the vendored copy of it.
//
// TIERS. M0-M4 are cluster-neutral: they read what the chart RENDERS. MA1-MA4 compare the render to
// the service's own declaration (its env, its committed edge list) and name no cluster. Nothing here
// talks to an API server — that is the live tier, and it reports rather than fails, exactly as
// chartstd's tier 3 does.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding is one rule's objection to one object.
type Finding struct {
	Rule   string
	Where  string
	Detail string
}

func (f Finding) String() string { return f.Rule + " " + f.Where + ": " + f.Detail }

// obj is one rendered Kubernetes object, as loosely as the rules need it.
type obj struct {
	Kind string
	Name string
	NS   string
	Spec map[string]any
	Raw  map[string]any
	Conc []map[string]any // container specs, for the Deployment env rules
}

// load decodes a rendered manifest stream. A render with no EventRegistration is an error rather than
// a pass: this tool's whole question is about Maestro wiring, so "nothing to check" means the caller
// pointed it at the wrong render.
func load(path string, r io.Reader) ([]obj, error) {
	dec := yaml.NewDecoder(r)
	var out []obj
	for {
		var d map[string]any
		if err := dec.Decode(&d); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(d) == 0 || d["kind"] == nil {
			continue
		}
		o := obj{Raw: d}
		o.Kind, _ = d["kind"].(string)
		md, _ := d["metadata"].(map[string]any)
		o.Name, _ = md["name"].(string)
		o.NS, _ = md["namespace"].(string)
		o.Spec, _ = d["spec"].(map[string]any)
		o.Conc = containers(d)
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: rendered no objects. A render that produces nothing means the "+
			"render command is wrong, not that the chart is clean", path)
	}
	return out, nil
}

// containers collects the container specs of a workload, which is where a service declares the
// audience it will accept.
func containers(d map[string]any) []map[string]any {
	spec, _ := d["spec"].(map[string]any)
	if spec == nil {
		return nil
	}
	tmpl, _ := spec["template"].(map[string]any)
	if tmpl == nil {
		return nil
	}
	podSpec, _ := tmpl["spec"].(map[string]any)
	if podSpec == nil {
		return nil
	}
	var out []map[string]any
	for _, key := range []string{"containers", "initContainers"} {
		list, _ := podSpec[key].([]any)
		for _, c := range list {
			if m, ok := c.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// envOf reads one container env value, literal only. A valueFrom ref is a cluster read, and this tier
// is deliberately cluster-neutral: the rule compares two declarations in the same render.
func envOf(c map[string]any, name string) string {
	list, _ := c["env"].([]any)
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok || fmt.Sprint(m["name"]) != name {
			continue
		}
		if v, ok := m["value"].(string); ok {
			return v
		}
		return ""
	}
	return ""
}

func serviceNames(objs []obj) map[string]bool {
	names := map[string]bool{}
	for _, o := range objs {
		if o.Kind == "Service" && o.Name != "" {
			names[o.Name] = true
		}
	}
	return names
}

func deploymentAudience(objs []obj) string {
	for _, o := range objs {
		for _, c := range o.Conc {
			if v := envOf(c, "LEARTECH_AUTH_AUDIENCE"); v != "" {
				return v
			}
		}
	}
	return ""
}

// baseURLHost takes the host out of a baseURL without a URL parser's strictness, because the failures
// this catches include "http://auth-service:8080" (no namespace at all), which is a parse failure to
// net/url only in the sense that it is wrong.
var baseURLRe = regexp.MustCompile(`^https?://([^/:]+)(?::[0-9]+)?(/|$)`)

func baseURLHost(baseURL string) (string, bool) {
	m := baseURLRe.FindStringSubmatch(strings.TrimSpace(baseURL))
	if m == nil {
		return "", false
	}
	host := m[1]
	if i := strings.Index(host, "."); i > 0 {
		host = host[:i]
	}
	return host, host != ""
}

// checkEventRegistrations applies the rules to one render.
//
// proven-by: TestM1_BaseURLNamesNoRenderedService
// proven-by: TestMA1_ConsumerAudienceDiffersFromTheDeployments
// proven-by: TestMA3_ProducerWithNoAnnotationKeys
// proven-by: TestM0_NoEventRegistrationInTheRenderIsAFailure
func checkEventRegistrations(objs []obj, prodAudiences map[string]bool) []Finding {
	var findings []Finding
	services := serviceNames(objs)
	audience := deploymentAudience(objs)

	var regs []obj
	for _, o := range objs {
		if o.Kind == "EventRegistration" {
			regs = append(regs, o)
		}
	}
	if len(regs) == 0 {
		findings = append(findings, Finding{"M0", "render",
			"no EventRegistration in the render. Point this at the chart that registers with Maestro, " +
				"or delete the invocation — a check over nothing reports green and means nothing"})
		return findings
	}

	for _, o := range regs {
		where := o.NS + "/" + o.Name
		if o.NS == "" {
			where = o.Name
		}
		baseURL, _ := o.Spec["baseURL"].(string)
		consumes, _ := o.Spec["consumes"].([]any)
		produces, _ := o.Spec["produces"].([]any)
		auth, _ := o.Spec["authentication"].(map[string]any)

		// M1 — the baseURL must name a Service this render emits.
		if len(consumes) > 0 {
			if strings.TrimSpace(baseURL) == "" {
				findings = append(findings, Finding{"M1", where,
					"it consumes events but sets no spec.baseURL, so Maestro has nowhere to dispatch"})
			} else if host, ok := baseURLHost(baseURL); !ok {
				findings = append(findings, Finding{"M1", where,
					fmt.Sprintf("spec.baseURL %q is not http(s)://host[:port]", baseURL)})
			} else if len(services) > 0 && !services[host] {
				findings = append(findings, Finding{"M1", where,
					fmt.Sprintf("spec.baseURL names Service %q, which this render does not emit (%s). "+
						"Maestro would dial a name that does not resolve", host, serviceList(services))})
			}
		}

		// M2 / M3 / M4 — the entries themselves.
		for _, c := range consumes {
			m, _ := c.(map[string]any)
			name, _ := m["name"].(string)
			endpoint, _ := m["endpoint"].(string)
			if strings.TrimSpace(name) == "" {
				findings = append(findings, Finding{"M2", where,
					"a consumes entry names no event, so it can never be dispatched to"})
			}
			if !strings.HasPrefix(endpoint, "/") {
				findings = append(findings, Finding{"M2", where,
					fmt.Sprintf("consumes %q has endpoint %q; Maestro appends it to baseURL, so it "+
						"needs a leading slash", name, endpoint)})
			}
		}
		for _, p := range produces {
			m, _ := p.(map[string]any)
			name, _ := m["name"].(string)
			keys, _ := m["annotationKeys"].([]any)
			if strings.TrimSpace(name) == "" {
				findings = append(findings, Finding{"M3", where,
					"a produces entry names no event, so it reads as coverage and is not"})
				continue
			}
			if len(keys) == 0 {
				findings = append(findings, Finding{"MA3", where,
					fmt.Sprintf("produces %q declares no annotationKeys. If the code sends any "+
						"annotation, Maestro refuses the announce: \"event is registered with "+
						"annotation keys [], but found [...]\"", name)})
			}
		}

		// M4 — the strategy Maestro accepts.
		strategy, _ := auth["strategy"].(string)
		if len(consumes) > 0 && strategy == "" {
			findings = append(findings, Finding{"M4", where,
				"it consumes events but sets no authentication.strategy; the CRD accepts only " +
					"\"internal\""})
		} else if strategy != "" && strategy != "internal" {
			findings = append(findings, Finding{"M4", where,
				fmt.Sprintf("authentication.strategy is %q; the CRD accepts only \"internal\"", strategy)})
		}

		// MA1 / MA2 — the CR's audience and the service's own.
		consumerAudience, _ := auth["audience"].(string)
		if len(consumes) == 0 || audience == "" {
			continue
		}
		if consumerAudience != "" && consumerAudience != audience {
			findings = append(findings, Finding{"MA1", where,
				fmt.Sprintf("authentication.audience is %q but the Deployment declares "+
					"LEARTECH_AUTH_AUDIENCE=%q. Maestro mints the first; the service verifies the "+
					"second, so every dispatch 401s", consumerAudience, audience)})
		}
		if consumerAudience == "" {
			findings = append(findings, Finding{"MA2", where,
				fmt.Sprintf("the Deployment enforces audience %q but the CR pins none, so dispatch "+
					"tokens arrive carrying the plain service audience — which works only because "+
					"this consumer accepts any audience", audience)})
		}

		// MA4 — the audience must be mintable by Maestro's own dispatch client.
		if consumerAudience != "" && prodAudiences != nil && !prodAudiences[consumerAudience] {
			findings = append(findings, Finding{"MA4", where,
				fmt.Sprintf("authentication.audience %q has no maestro-internal-services edge in "+
					"the authgraph, so the mint is refused: \"Requested audience '%s' has not been "+
					"whitelisted by the OAuth 2.0 Client\"", consumerAudience, consumerAudience)})
		}
	}
	return findings
}

func serviceList(services map[string]bool) string {
	var names []string
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) > 6 {
		names = append(names[:6], fmt.Sprintf("… %d more", len(names)-6))
	}
	return strings.Join(names, ", ")
}

// loadAudiences reads the vendored list of audiences `maestro-internal-services` may mint, which is the
// authgraph's dispatch edges and nothing else. One audience per line, `#` comments allowed.
//
// A MISSING file is not an empty list: it means the comparison cannot be made, and a rule that cannot
// be evaluated must say so rather than report a pass.
//
// proven-by: TestLoadAudiences_MissingFileIsAnError
func loadAudiences(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the authgraph's maestro audiences from %s: %w", path, err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no audiences, so MA4 would pass over an empty set", path)
	}
	return out, nil
}

func main() {
	var renders multiFlag
	var charts multiFlag
	flag.Var(&renders, "render", "a `helm template` / helmfile render to judge; repeatable")
	flag.Var(&charts, "chart", "a chart directory, judged from its render if -render is given (source rules)")
	audiences := flag.String("audiences", "", "file of audiences maestro-internal-services may mint "+
		"(the authgraph's dispatch edges); omit to skip MA4")
	require := flag.String("require", "", "comma-separated commands that must be on PATH")
	notesOnly := flag.Bool("notes", false, "print each finding as a note and always exit 0")
	flag.Parse()

	for _, tool := range strings.Split(*require, ",") {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %s is not on PATH, so this check could not run. "+
				"That is a missing tool, not a clean chart.\n", tool)
			os.Exit(2)
		}
	}

	if len(renders) == 0 {
		fmt.Fprintln(os.Stderr, "maestrostd: -render is required (this tool judges a RENDER, "+
			"because the wiring only exists after templating)")
		os.Exit(2)
	}

	allowed, err := loadAudiences(*audiences)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(2)
	}

	var all []Finding
	total := 0
	for _, path := range renders {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(2)
		}
		objs, err := load(path, f)
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(2)
		}
		regs := 0
		for _, o := range objs {
			if o.Kind == "EventRegistration" {
				regs++
			}
		}
		if regs == 0 {
			fmt.Printf("  note  %s: no EventRegistration here, so this render is not the one to judge\n", path)
			continue
		}
		total += regs
		all = append(all, checkEventRegistrations(objs, allowed)...)
	}

	if total == 0 {
		fmt.Println("maestrostd: no EventRegistration in any render given, so nothing was checked.")
		fmt.Println("           If this chart should register with Maestro, the render is wrong;")
		fmt.Println("           if it should not, delete the maestrostd invocation.")
		return
	}

	if len(all) == 0 {
		fmt.Printf("maestrostd: %d EventRegistration(s) from %d render(s), no findings\n", total, len(renders))
		return
	}

	for _, f := range all {
		fmt.Printf("  %s\n", f)
	}
	fmt.Printf("\nmaestrostd: %d finding(s) across %d EventRegistration(s)\n", len(all), total)
	if *notesOnly {
		return
	}
	os.Exit(1)
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	if v == "" {
		return nil
	}
	*m = append(*m, v)
	return nil
}
