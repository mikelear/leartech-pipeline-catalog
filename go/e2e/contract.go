package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ContractFile is where a repo declares what its preview is. It sits next to the
// pack, so a reviewer sees the declaration and the checks in one diff.
const ContractFile = "end2end/preview.yaml"

// DeclaredContract is the on-disk shape. Pointers where "absent" and "empty"
// mean different things: `gate: ""` is a typo, no `gate:` key is a default, and
// only a pointer distinguishes them.
type DeclaredContract struct {
	Preview struct {
		HealthPath string `yaml:"healthPath"`
		// HealthPathSource says where the path came from when it is not simply
		// declared: "chart" means the chart's readinessProbe is authoritative and
		// this file only records it. Present so a repo that genuinely has no
		// declarable path still has to say so.
		// declare one still has to say so out loud.
		HealthPathSource string `yaml:"healthPathSource"`
		Service          string `yaml:"service"`
		Port             int    `yaml:"port"`
		VersionAnchor    *bool  `yaml:"versionAnchor"`
		Gate             string `yaml:"gate"`
		Probes           []struct {
			URL  string `yaml:"url"`
			Want int    `yaml:"want"`
			Note string `yaml:"note"`
		} `yaml:"probes"`
		// Setup replaces hand-written setup scripts with the catalog's menu. Each entry
		// names a verb and its parameters; see setup.go for the closed set.
		Setup []DeclaredSetup `yaml:"setup"`
		// Bespoke is the declared exception: a script the menu does not cover, in a
		// preview.
		// only, and it carries a reason a reviewer can weigh.
		Bespoke []Bespoke `yaml:"bespoke"`
	} `yaml:"preview"`
	UI struct {
		Probe string `yaml:"probe"`
	} `yaml:"ui"`
}

// Contract is DeclaredContract with every default resolved, so nothing
// downstream has to decide what an absent key meant.
type Contract struct {
	Path          string
	HealthPath    string
	HealthFrom    string
	Service       string
	Port          int
	VersionAnchor bool
	Gate          GateMode
	Probes        []Probe
	Seeds         []string
	Setup         []Setup
	Bespoke       []Bespoke
	UIProbe       string
}

// Probe is one URL that has to answer with a status before a suite runs. The gate
// chart already has this concept (HTTP_PROBES); it lives in the declaration so
// both lanes get it, not just the repos that deploy the gate.
type Probe struct {
	URL  string
	Want int
	Note string
}

// GateMode is what the repo says about the readiness gate, so neither lane has to
// spend 60-180 seconds discovering it.
type GateMode string

const (
	// GateAuto probes once, briefly, and says which path it took.
	GateAuto GateMode = "auto"
	// GateRequired fails fast if the gate is not there: for a repo that relies on
	// the gate's extra probes, silently falling back would test something weaker.
	GateRequired GateMode = "required"
	// GateAbsent skips detection entirely.
	GateAbsent GateMode = "absent"
)

// defaults applied when the declaration omits a key.
const (
	DefaultHealthPath = "/health"
	DefaultGate       = GateAuto
	DefaultUIProbe    = "ingress"
)

// knownEnvKeys are the only END2END_* / E2E_* variables the catalog tasks read.
// A consumer setting anything else is a silent no-op today, which is how a repo
// came to believe it had declared /health while the run probed /health/live.
var knownEnvKeys = map[string]bool{
	"END2END_HEALTH_PATH":    true,
	"END2END_HEALTH_TIMEOUT": true,
	"E2E_HEALTH_TIMEOUT":     true,
	"GATE_DETECT_TIMEOUT":    true,
	"END2END_FLEET_BASE":     true,
	"E2E_COVERAGE":           true,
	"STAGING_URL":            true,
	"UI_PROBE":               true,
	"PLAYWRIGHT_WORKERS":     true,
	"E2E_UI_TIMEOUT":         true,
}

// LoadContract reads and validates the declaration. It returns the resolved
// contract even when there are findings, so a caller can explain what it would
// have done — an error that throws away the parse is one that resists debugging.
func LoadContract(repoDir string) (*Contract, []string) {
	path := filepath.Join(repoDir, ContractFile)
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the repo under test
	if err != nil {
		return nil, []string{fmt.Sprintf(
			"%s: not readable (%v). A repo with an end2end pack must declare its preview: "+
				"healthPath, and optionally service/port/gate/setup/bespoke. "+
				"See docs/end2end-preview.md.", ContractFile, err)}
	}
	var d DeclaredContract
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return nil, []string{fmt.Sprintf("%s: does not parse: %v", ContractFile, err)}
	}

	c := &Contract{Path: path, UIProbe: DefaultUIProbe}
	var findings []string

	switch {
	case d.Preview.HealthPath != "":
		c.HealthPath = d.Preview.HealthPath
		c.HealthFrom = "declared"
	case d.Preview.HealthPathSource == "chart":
		// The wait already prefers the chart's readinessProbe when it can read it;
		// this records that the repo knows and accepts that.
		c.HealthPath = ""
		c.HealthFrom = "chart"
	default:
		c.HealthPath = DefaultHealthPath
		c.HealthFrom = "default"
		findings = append(findings, fmt.Sprintf(
			"%s: preview.healthPath is not set, so the wait would probe %q. "+
				"Declare the path (or `healthPathSource: chart` if the chart's readinessProbe "+
				"is the truth) — guessing is how a repo came to probe /health/live on a preview "+
				"that serves /health.", ContractFile, DefaultHealthPath))
	}
	if c.HealthPath != "" && !strings.HasPrefix(c.HealthPath, "/") {
		findings = append(findings, fmt.Sprintf(
			"%s: preview.healthPath %q does not start with / — it is appended to the base URL, "+
				"so it would produce %q.", ContractFile, c.HealthPath, "http://host"+c.HealthPath))
	}

	c.Service = d.Preview.Service
	c.Port = d.Preview.Port
	if d.Preview.VersionAnchor != nil {
		c.VersionAnchor = *d.Preview.VersionAnchor
	} else {
		c.VersionAnchor = true
	}

	switch strings.ToLower(strings.TrimSpace(d.Preview.Gate)) {
	case "":
		c.Gate = DefaultGate
	case string(GateAuto):
		c.Gate = GateAuto
	case string(GateRequired):
		c.Gate = GateRequired
	case string(GateAbsent):
		c.Gate = GateAbsent
	default:
		c.Gate = DefaultGate
		findings = append(findings, fmt.Sprintf(
			"%s: preview.gate %q is not one of auto|required|absent, so it would be treated as "+
				"auto and spend its detect window finding out.", ContractFile, d.Preview.Gate))
	}

	for i, p := range d.Preview.Probes {
		if strings.TrimSpace(p.URL) == "" {
			findings = append(findings, fmt.Sprintf(
				"%s: preview.probes[%d] has no url.", ContractFile, i))
			continue
		}
		want := p.Want
		if want == 0 {
			want = 200
		}
		if want < 100 || want > 599 {
			findings = append(findings, fmt.Sprintf(
				"%s: preview.probes[%d].want %d is not an HTTP status.", ContractFile, i, want))
		}
		c.Probes = append(c.Probes, Probe{URL: p.URL, Want: want, Note: p.Note})
	}

	for i, raw := range d.Preview.Setup {
		step, stepFindings := resolveSetup(raw)
		c.Setup = append(c.Setup, step)
		for _, f := range stepFindings {
			findings = append(findings, fmt.Sprintf("%s: setup[%d]: %s", ContractFile, i, f))
		}
	}
	for i, b := range d.Preview.Bespoke {
		if strings.TrimSpace(b.Script) == "" {
			findings = append(findings, fmt.Sprintf(
				"%s: bespoke[%d] has no `script`. A declared exception names the file it "+
					"covers, or the declaration excuses nothing.", ContractFile, i))
			continue
		}
		if strings.TrimSpace(b.Reason) == "" {
			findings = append(findings, fmt.Sprintf(
				"%s: bespoke[%d] (%s) has no `reason`. Without one a reviewer cannot tell "+
					"an unavoidable procedure from an unfinished menu verb, which is how a "+
					"repo ends up with three of them.", ContractFile, i, b.Script))
		}
		// A bespoke script against staging is refused outright: the one script in the
		// estate that repairs shared-state damage exists because another repo's bespoke
		// setup poisoned a shared fixture.
		if strings.EqualFold(strings.TrimSpace(b.Where), string(WhereStaging)) ||
			strings.EqualFold(strings.TrimSpace(b.Where), string(WhereBoth)) {
			findings = append(findings, fmt.Sprintf(
				"%s: bespoke[%d] (%s) declares where: %s. A bespoke script runs in a preview "+
					"only — staging is persistent and shared, and runs on every Observer "+
					"dispatch, so an unproven procedure there can poison a sibling repo's "+
					"fixtures. Use a menu verb, which is idempotent by construction.",
				ContractFile, i, b.Script, b.Where))
		}
		c.Bespoke = append(c.Bespoke, b)
	}
	if strings.EqualFold(strings.TrimSpace(d.UI.Probe), "service") {
		c.UIProbe = "service"
	}

	// A healthPath of "" with HealthFrom "chart" is the only case where the wait
	// reads the path from the deployment; everything else is concrete here.
	return c, findings
}

// SeedNameLooksLikeACheck reports whether a script listed in the pack looks like a
// check rather than a setup step.
//
// WHY. The UI lane currently runs EVERY 0X-*.sh as a "seed" with failures
// swallowed. On leartech-auth-service that is 21 scripts, including the whole
// functional suite, so a genuine failure is printed as "(seed script exited
// non-zero — continuing)". A seed is setup; a check is a check.
func SeedNameLooksLikeACheck(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	for _, marker := range []string{"register", "seed", "setup", "reset", "fixture", "bootstrap"} {
		if strings.Contains(base, marker) {
			return false
		}
	}
	return true
}

// String renders the resolved contract, defaults included, for `e2e explain`.
func (c *Contract) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "contract:      %s\n", c.Path)
	hp := c.HealthPath
	if hp == "" {
		hp = "(from the deployment's readinessProbe)"
	}
	fmt.Fprintf(&b, "healthPath:    %s   [%s]\n", hp, c.HealthFrom)
	if c.Service != "" {
		fmt.Fprintf(&b, "service:       %s", c.Service)
		if c.Port != 0 {
			fmt.Fprintf(&b, " (port %d)", c.Port)
		}
		fmt.Fprintln(&b)
	} else {
		b.WriteString("service:       (discovered by app.kubernetes.io/name, then by name)\n")
	}
	fmt.Fprintf(&b, "versionAnchor: %v\n", c.VersionAnchor)
	fmt.Fprintf(&b, "gate:          %s\n", c.Gate)
	fmt.Fprintf(&b, "ui.probe:      %s\n", c.UIProbe)
	if len(c.Probes) == 0 {
		b.WriteString("probes:        none\n")
	} else {
		b.WriteString("probes:\n")
		for _, p := range c.Probes {
			fmt.Fprintf(&b, "  - %s -> %d", p.URL, p.Want)
			if p.Note != "" {
				fmt.Fprintf(&b, "   (%s)", p.Note)
			}
			b.WriteString("\n")
		}
	}
	if len(c.Setup) == 0 {
		b.WriteString("setup:         none\n")
	} else {
		b.WriteString("setup (from the catalog's menu):\n")
		for _, line := range setupPlan(c.Setup, WherePreview) {
			fmt.Fprintf(&b, "  - %s\n", line)
		}
	}
	if len(c.Bespoke) > 0 {
		b.WriteString("bespoke (preview only, declared exception):\n")
		for _, bs := range c.Bespoke {
			fmt.Fprintf(&b, "  - %s: %s\n", bs.Script, bs.Reason)
		}
	}
	if len(c.Seeds) == 0 {
		b.WriteString("seeds:         none\n")
	} else {
		sorted := append([]string(nil), c.Seeds...)
		sort.Strings(sorted)
		b.WriteString("seeds:\n")
		for _, s := range sorted {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	return b.String()
}
