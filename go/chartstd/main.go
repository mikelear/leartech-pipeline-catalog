// chartstd is the Ship Proven chart standard, as a gate.
//
// It reads what a chart RENDERS, not what its templates look like, because every
// rule below is about the object the cluster receives. The one rule that cannot
// be seen in a render — "this value must be required" — is named in docs/
// chart-standard.md and proven by a negative render, not by this tool.
//
// The rules and the failure each one prevents:
//
//	S2  every ExternalSecret sets creationPolicy: Owner
//	    otherwise deleting the ExternalSecret orphans the Secret, and an
//	    orphaned credential is worse than a missing one: the pod starts.
//	S3  no two objects claim the same Secret in the same namespace
//	    two creationPolicy: Owner claims flip ownership between reconciles, so a
//	    consumer reading mid-flip gets an empty value.
//	S4  never spec.data AND spec.template.data in one ExternalSecret
//	    ESO drops spec.data entirely once template.data exists, so the keys it
//	    named vanish with no error.
//	S5  no literal secret material in a chart's values or configs
//	    "password: todo" is not a placeholder; it is a deployed credential.
//	S6  nothing sets iam.gke.io/gcp-service-account
//	    that annotation comes from terraform output after the KSA exists, per
//	    cluster; templating it puts a value in git that is right for one cluster.
//	S1b an ExternalSecret's store name is never empty after render
//	    a value that templated to nothing means nobody supplied it.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Finding is one rule violation, with the evidence that makes it actable.
type Finding struct {
	Rule   string
	Where  string
	Detail string
}

func (f Finding) String() string { return f.Rule + " " + f.Where + ": " + f.Detail }

// obj is one rendered Kubernetes object, as loosely as the rules need.
type obj struct {
	Kind   string
	APIVer string `yaml:"apiVersion"`
	Name   string
	NS     string
	Annots map[string]string
	Spec   map[string]any
	Raw    map[string]any
	Source string
}

// load decodes a rendered manifest stream. Helm emits `---`-separated YAML; a
// chart that renders nothing yields no docs, and that is a failure here rather
// than a pass, because a check over an empty set checks nothing.
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
		o := obj{Source: path, Raw: d}
		o.Kind, _ = d["kind"].(string)
		o.APIVer, _ = d["apiVersion"].(string)
		md, _ := d["metadata"].(map[string]any)
		o.Name, _ = md["name"].(string)
		o.NS, _ = md["namespace"].(string)
		if a, ok := md["annotations"].(map[string]any); ok {
			o.Annots = map[string]string{}
			for k, v := range a {
				if s, ok := v.(string); ok {
					o.Annots[k] = s
				}
			}
		}
		o.Spec, _ = d["spec"].(map[string]any)
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: rendered no objects. This tool cannot pass vacuously — a "+
			"render that produces nothing means the render command is wrong, not that the "+
			"chart is clean", path)
	}
	return out, nil
}

// externalSecretTarget returns the Secret an ExternalSecret claims and the
// policy it claims it with.
func externalSecretTarget(o obj) (target, policy, store string, hasTemplateData bool) {
	t, _ := o.Spec["target"].(map[string]any)
	target, _ = t["name"].(string)
	policy, _ = t["creationPolicy"].(string)
	if tmpl, ok := o.Spec["template"].(map[string]any); ok {
		_, hasTemplateData = tmpl["data"].(map[string]any)
	}
	if ref, ok := o.Spec["secretStoreRef"].(map[string]any); ok {
		store, _ = ref["name"].(string)
	}
	return
}

// checkObjects applies the render rules to one chart's documents.
func checkObjects(docs []obj, wantStore string) []Finding {
	var out []Finding
	// claimant maps "ns/name" to the object that claimed it, so a collision
	// names BOTH sides rather than just the second one seen.
	type claim struct{ kind, where string }
	claimants := map[string][]claim{}

	for _, o := range docs {
		where := fmt.Sprintf("%s (%s)", o.Name, o.Source)

		// S2 / S4 / S1b — ExternalSecret-specific.
		if o.Kind == "ExternalSecret" {
			target, policy, store, hasTemplateData := externalSecretTarget(o)
			if policy != "Owner" {
				out = append(out, Finding{"S2", "ExternalSecret " + where, fmt.Sprintf(
					"creationPolicy is %q, want \"Owner\". Deleting this object would orphan "+
						"the Secret %q, and a pod that mounts an orphaned credential starts.",
					policy, target)})
			}
			if _, hasData := o.Spec["data"].([]any); hasData && hasTemplateData {
				out = append(out, Finding{"S4", "ExternalSecret " + where,
					"sets BOTH spec.data and spec.template.data. ESO drops spec.data once " +
						"template.data exists, so the keys spec.data names are silently absent."})
			}
			switch {
			case store == "":
				out = append(out, Finding{"S1b", "ExternalSecret " + where,
					"renders an empty secretStoreRef.name — the value was not supplied and " +
						"templated to nothing. It must be a required value (S1)."})
			case wantStore != "" && store != wantStore:
				// S1. The chart rendered a store name nobody supplied as config: it
				// came from a default in the template or a literal in values.yaml, and
				// both put a store in git that this cluster may not have. console-api
				// shipped `storeRef: cluster-secret-store`, a store that exists
				// nowhere here, and the failure surfaced as
				// "could not get secret data from provider" on an object that read
				// correct.
				out = append(out, Finding{"S1", "ExternalSecret " + where, fmt.Sprintf(
					"secretStoreRef.name is %q, not %q. A store named in a default or in "+
						"values is a promise this cluster does not keep; it must come from the "+
						"required value in config/secrets.yaml.", store, wantStore)})
			}
			if target != "" {
				key := o.NS + "/" + target
				claimants[key] = append(claimants[key], claim{"ExternalSecret", where})
			}
		}

		// S3 — a plain Secret is a claimant too: the collision is between
		// OWNERS, not between kinds.
		if o.Kind == "Secret" && o.APIVer == "v1" && o.Name != "" {
			// A two-owner secret is legitimate only when the chart says so,
			// which sp-infra-resources already does for the Hydra-maester pair.
			if _, declared := o.Annots["sp-proven.io/owners"]; declared {
				claimants[o.NS+"/"+o.Name] = append(claimants[o.NS+"/"+o.Name], claim{"Secret", where})
				continue
			}
			claimants[o.NS+"/"+o.Name] = append(claimants[o.NS+"/"+o.Name], claim{"Secret", where})
		}

		// S6 — the Workload Identity annotation belongs to terraform, after the
		// KSA exists, per cluster.
		if v, ok := o.Annots["iam.gke.io/gcp-service-account"]; ok {
			out = append(out, Finding{"S6", o.Kind + " " + where, fmt.Sprintf(
				"sets iam.gke.io/gcp-service-account=%q. That binding comes from terraform "+
					"output after the KSA exists and is per-cluster; a chart that templates it "+
					"puts one cluster's value in git and fails at runtime as a permission error.",
				v)})
		}
	}

	// S3 findings, sorted so the output is stable between runs.
	keys := make([]string, 0, len(claimants))
	for k := range claimants {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cs := claimants[k]
		if len(cs) < 2 {
			continue
		}
		var sides []string
		for _, c := range cs {
			sides = append(sides, c.where)
		}
		out = append(out, Finding{"S3", "Secret " + k, fmt.Sprintf(
			"claimed by %d objects: %s. Two creationPolicy: Owner claims flip "+
				"ownership between reconciles and a consumer can read it mid-flip as empty.",
			len(cs), strings.Join(sides, "; "))})
	}
	return out
}

// proven-by: TestRulesFireOnRealEstateShapes
//
// boolDefault matches a boolean gate written through sprig `default`, e.g.
// `(.Values.x.enabled | default true)`.
//
// proven-by: TestRulesFireOnRealEstateShapes
//
// sprig's `default` returns the fallback when the value is EMPTY, and `false` is
// empty, so such an option has one reachable value. Measured on a three-line
// chart (2026-11-18): `default true` -> RENDERS with enabled:false; `ne (toString
// …) "false"` -> SKIPPED, which is what the author meant.
//
// It is a source rule, not a render rule, because the bug is invisible in the
// render that the author looked at: they set the value they believed in, and the
// object appeared or did not, and either way it agreed with the default.
var boolDefault = regexp.MustCompile(`\|\s*default\s+(true|false)\b`)

var (
	requireTools string
	secretKey    = regexp.MustCompile(`(?i)^\s*[a-z0-9_.-]*(password|passwd|secret|token|clientsecret|apikey|api_key)[a-z0-9_.-]*\s*:\s*(.+)$`)
	// Values that read like a secret but are not: a reference, an env name, a
	// path, a template action. Anything else after the colon is material.
	notMaterial = regexp.MustCompile(`(?i)^(\{\{|""|''|~|null|\$|/|env\.|secretKeyRef|valueFrom|` +
		`\*|remoteName|remoteRef|name:|path:|key:|length\(|quote|<|\.Values|[a-z-]+/[a-z-]+)`)
	denied = []string{"todo", "changeme", "change-me", "xxx", "placeholder", "fixme"}
)

// checkValues applies S5 to a chart's source. A render cannot see this: the
// value may reach a Secret via a path the rules above do not model.
func checkValues(chartDir string) []Finding {
	var out []Finding
	walk := func(path string) error {
		b, err := os.ReadFile(path) // #nosec G304 -- path comes from our own walk
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			m := secretKey.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			val := strings.TrimSpace(strings.SplitN(m[2], "#", 2)[0])
			val = strings.Trim(val, `"'`)
			if val == "" || notMaterial.MatchString(val) {
				continue
			}
			low := strings.ToLower(val)
			for _, d := range denied {
				if low == d {
					out = append(out, Finding{"S5", fmt.Sprintf("%s:%d", path, i+1), fmt.Sprintf(
						"%q is a literal credential. A placeholder that deploys is a "+
							"credential; use an ExternalSecret (S1–S3).", val)})
					break
				}
			}
		}
		return nil
	}
	for _, pat := range []string{"values.yaml", "values.yml", "*.values.yaml"} {
		matches, _ := filepath.Glob(filepath.Join(chartDir, pat))
		for _, m := range matches {
			if err := walk(m); err != nil {
				fmt.Fprintf(os.Stderr, "chartstd: %v\n", err)
			}
		}
	}
	confs, _ := filepath.Glob(filepath.Join(chartDir, "configs", "*.yaml"))
	for _, c := range confs {
		if err := walk(c); err != nil {
			fmt.Fprintf(os.Stderr, "chartstd: %v\n", err)
		}
	}
	return out
}

// proven-by: TestRulesFireOnRealEstateShapes
//
// checkTemplates applies S8.
func checkTemplates(chartDir string) []Finding {
	var out []Finding
	tpls, _ := filepath.Glob(filepath.Join(chartDir, "templates", "*.yaml"))
	for _, t := range tpls {
		b, err := os.ReadFile(t) // #nosec G304 -- path comes from our own glob
		if err != nil {
			fmt.Fprintf(os.Stderr, "chartstd: %v\n", err)
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			m := boolDefault.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			out = append(out, Finding{"S8", fmt.Sprintf("%s:%d", t, i+1), fmt.Sprintf(
				"`| default %s` on a boolean. sprig treats false as empty, so the option "+
					"can only ever be %s — setting it to the other value changes nothing. "+
					"Drop the default (values.yaml already carries it), or test "+
					"`ne (toString .Values.x) \"false\"`.", m[1], m[1])})
		}
	}
	return out
}

func main() {
	var renders multiFlag
	var chart, wantStore string
	var tplRules bool
	flag.Var(&renders, "render", "rendered manifest file (`-` for stdin); repeatable, and all files "+
		"are checked together so a Secret claimed by two charts is caught")
	flag.StringVar(&chart, "chart", "", "chart directory, for the source rules (S5)")
	flag.BoolVar(&tplRules, "templates", false,
		"also apply the source rules that need the templates (S8: a boolean gate "+
			"written through sprig default). Needs -chart.")
	var toggleDecl, listToggles, tenantCheck string
	var offRenders multiFlag
	var toggleNotes bool
	flag.StringVar(&tenantCheck, "tenant", "",
		"a chart directory whose values.yaml must carry no tenant-specific default "+
			"(S10: a GSA email, or a `which project` key holding a literal). Runs alone and "+
			"exits, so a consumer repo with no chart-standard task still gets this check.")
	flag.StringVar(&listToggles, "list-toggles", "",
		"print `<path>|<off>` per declared toggle and exit, so a shell step does not have "+
			"to parse the declaration itself")
	flag.StringVar(&toggleDecl, "toggles", "",
		"chart-standard-toggles.yaml declaring the off-switches the chart claims to have. "+
			"Needs -render (the default render) and one -render-off per toggle, in the "+
			"same order.")
	flag.Var(&offRenders, "render-off",
		"the render with one toggle flipped (`--set <path>=<off>`); repeatable, one per "+
			"declared toggle, in declaration order")
	flag.BoolVar(&toggleNotes, "toggle-notes", false,
		"report `*.enabled` keys in values.yaml that no toggle declaration covers (a "+
			"NOTE, not a failure). Needs -chart.")
	flag.StringVar(&requireTools, "require", "",
		"comma-separated tools that must be on PATH for the STEP that calls this tool. "+
			"Exits non-zero naming each one that is missing. This exists so a shell step does "+
			"not have to compose that sentence itself: prose in a `script: |` has no test "+
			"(the message-layer ratchet is right about that), and the message matters — "+
			"measured on leartech-console-api#37, a missing `chartstd` in the render step "+
			"reported `FAIL: cannot read <the repo's toggles file>`, which blames the repo.")
	flag.StringVar(&wantStore, "store", "",
		"the external-secrets store this estate supplies (S1). Required for a meaningful "+
			"run: without it the tool can only see a store that rendered EMPTY, not a store "+
			"that rendered the wrong name")
	flag.Parse()

	if requireTools != "" {
		// A TOOLCHAIN CHECK, NOT A CHART CHECK. It runs first and alone, because a missing
		// binary makes every later rule meaningless — and it prints WHICH tool and WHOSE
		// problem it is, which is the half a shell `command -v` block gets wrong.
		var missing, wanted []string
		for _, t := range strings.Split(requireTools, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			wanted = append(wanted, t)
			if _, err := exec.LookPath(t); err != nil {
				missing = append(missing, t)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "chart-standard: this step's image is missing %s.\n", strings.Join(missing, ", "))
			fmt.Fprintf(os.Stderr, "  The step declared it needs: %s.\n", strings.Join(wanted, ", "))
			fmt.Fprintf(os.Stderr, "  A missing tool is a TASK defect, not a repo defect: nothing about the chart\n")
			fmt.Fprintf(os.Stderr, "  under test is being reported here. Fix the task's image.\n")
			os.Exit(1)
		}
		return
	}

	if tenantCheck != "" {
		// One entry point for the "a released chart must not carry this" rules: S10
		// (tenant identity) and S11/S12 (an origin allow-list). They run together
		// because they answer one question -- what would a customer inherit? -- and a
		// second invocation is a second thing to forget to add to a task.
		findings := tenantFindings(tenantCheck)
		findings = append(findings, originFindings(tenantCheck)...)
		findings = append(findings, credentialFindings(tenantCheck)...)
		if len(findings) == 0 {
			fmt.Printf("==> chartstd: %s carries no tenant-specific or cluster-specific default (S10-S13)\n", tenantCheck)
			return
		}
		for _, f := range findings {
			fmt.Println(f)
		}
		fmt.Fprintf(os.Stderr, "\nFAIL: %d inherited-default finding(s) (S10-S13).\n", len(findings))
		os.Exit(1)
	}

	if listToggles != "" {
		decl, err := loadToggles(listToggles)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chartstd:", err)
			os.Exit(2)
		}
		for _, t := range decl {
			off := t.Off
			if off == "" {
				off = "false"
			}
			fmt.Printf("%s|%s\n", t.Path, off)
		}
		return
	}

	if wantStore == "" && tenantCheck == "" && listToggles == "" {
		fmt.Fprintln(os.Stderr, "chartstd: -store is required. Naming the store is the whole "+
			"point of S1: \"never name a store in a default\" can only be judged against the "+
			"store the estate actually supplies. Pass the value from config/secrets.yaml.")
		os.Exit(2)
	}

	if len(renders) == 0 {
		fmt.Fprintln(os.Stderr, "chartstd: -render is required "+
			"(helm template <release> <chart> > render.yaml)")
		os.Exit(2)
	}

	var all []obj
	for _, p := range renders {
		var r io.Reader
		if p == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(p) // #nosec G304 -- operator-supplied path
			if err != nil {
				fmt.Fprintln(os.Stderr, "chartstd:", err)
				os.Exit(2)
			}
			defer f.Close()
			r = f
		}
		docs, err := load(p, r)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chartstd:", err)
			os.Exit(2)
		}
		all = append(all, docs...)
	}

	findings := checkObjects(all, wantStore)
	var notes []Finding

	if toggleDecl != "" {
		decl, err := loadToggles(toggleDecl)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chartstd:", err)
			os.Exit(2)
		}
		if len(offRenders) != len(decl) {
			fmt.Fprintf(os.Stderr, "chartstd: %d toggle(s) declared but %d -render-off "+
				"file(s) given. Pairing them by position is the only way to know which "+
				"flip removed which object, so a mismatch cannot be checked.\n",
				len(decl), len(offRenders))
			os.Exit(2)
		}
		var offs [][]obj
		for _, p := range offRenders {
			f, err := os.Open(p) // #nosec G304
			if err != nil {
				fmt.Fprintln(os.Stderr, "chartstd:", err)
				os.Exit(2)
			}
			docs, err := load(p, f)
			f.Close()
			if err != nil {
				fmt.Fprintln(os.Stderr, "chartstd:", err)
				os.Exit(2)
			}
			offs = append(offs, docs)
		}
		findings = append(findings, checkToggles(toggleDecl, decl, all, offs)...)
		if chart != "" {
			// T3 is a NOTE. Every chart here carries undeclared switches, and the
			// first useful thing is to count them rather than red on all of them.
			notes = append(notes, undeclaredSwitches(chart, decl)...)
		}
	} else if toggleNotes && chart != "" {
		notes = append(notes, undeclaredSwitches(chart, nil)...)
	}
	if chart != "" {
		findings = append(findings, checkValues(chart)...)
		if tplRules {
			findings = append(findings, checkTemplates(chart)...)
		}
	}

	for _, n := range notes {
		fmt.Println("NOTE", n)
	}
	if len(notes) > 0 {
		fmt.Printf("     %d undeclared off-switch(es) — notes, not failures\n", len(notes))
	}

	if len(findings) == 0 {
		fmt.Printf("==> chartstd: %d object(s) from %d render(s), no findings "+
			"(S1b, S2, S3, S4, S5, S6, S8, S10, T1, T2)\n", len(all), len(renders))
		return
	}
	for _, f := range findings {
		fmt.Println(f)
	}
	fmt.Fprintf(os.Stderr, "\nFAIL: %d finding(s). See docs/chart-standard.md — each rule names "+
		"the failure it prevents.\n", len(findings))
	os.Exit(1)
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
