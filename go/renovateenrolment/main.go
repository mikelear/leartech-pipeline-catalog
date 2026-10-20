// renovateenrolment — a repo Renovate never visits has no dependency updates
// and no CVE alerts, and its renovate.json looks perfectly correct.
//
// WHY THIS EXISTS. On 2026-09-15 the cluster Renovate config listed 17
// repositories. The estate deployed 34 services. Of the 26 deployed repos that
// were missing, 24 already carried a renovate.json extending the shared preset
// — including a packageRule matching ^github.com/mikelear/leartech-go-common
// with automerge enabled. That rule had never fired anywhere, because Renovate
// does not read a repo it was not told about. Seven services sat on go-common
// v1.2.0 while the library was at v1.3.3; every historic bump in those repos
// had been raised by hand.
//
// The cluster config's own header already records this class of failure biting
// once before: an earlier "only allow @mikelear/*" override silently
// neutralised every per-repo config, and six months of Docker base images went
// untracked until a mutating playwright tag broke a consumer pipeline. That was
// fixed by splitting infra from policy. The repositories list is the same
// disease in a different organ — configuration that reads as correct and does
// nothing.
//
// WHY vulnerabilityAlerts MAKES THIS SECURITY-RELEVANT. The cluster config sets
// vulnerabilityAlerts.enabled with a comment calling it "defence-in-depth
// beyond whatever per-repo config decides". It is not: it applies only to repos
// Renovate visits. The 26 absent repos received no CVE-driven PRs at all.
//
// THE RULE. Every deployed release that is one of ours must be either enrolled
// in the repositories list, or exempt with a stated reason. Silence is not an
// option — that is the whole point. An exemption is cheap and permanent; an
// omission is invisible and rots.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not check that Renovate is running,
// that a PR was opened, or that a version is current. It checks only that the
// repo is in scope at all. A repo can be enrolled and still fall behind — that
// is a different defect with a different fix, and conflating them would make
// this check unfixable.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// A helmfile release's chart. In production these are list items, so the
	// leading "- " is not optional in practice:
	//
	//     - chart: dev/leartech-gate            <- ours
	//     - chart: bitnami-oci/postgresql       <- upstream
	//     - chart: ../../charts/security-scans  <- infra chart inside this repo
	//
	// An earlier version of this regex omitted the "-?" and matched zero of the
	// 110 real helmfiles while matching every hand-written fixture. The counts
	// guard below is what caught it.
	chartRef = regexp.MustCompile(`(?m)^\s*-?\s*chart:\s*["']?(\S+?)["']?\s*$`)

	// The cluster config's repositories list entries: "mikelear/leartech-gate".
	enrolledRef = regexp.MustCompile(`["']mikelear/([A-Za-z0-9._-]+)["']`)

	// "dev/" is the jx dev chart repository — charts THIS estate builds and
	// releases. Every other alias is a third party, and "../" is an infra chart
	// living inside the GitOps repo with no service repo behind it.
	//
	// This beats matching on a leartech-* name prefix, which was the first
	// attempt: all 34 dev/ charts resolve to a repo we own, including
	// next-generation-lending-website, future-lending-ui, hello-go7 and
	// lighthouse, which a name prefix misses.
	oursChart = "dev/"
)

type finding struct {
	release string
	why     string
	fix     string
}

// repeatable collects -deployed-root, which may be given more than once.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	root := flag.String("gitops-root", ".", "GitOps repo root holding the Renovate repositories list")
	// Renovate enrolment is GLOBAL — one instance, scoped by a GitHub repo list
	// — while the deployed set spans both clusters. Scanning only the repo that
	// happens to host the list makes every service deployed to the OTHER
	// cluster invisible: on 2026-09-15 that was leartech-ba-service,
	// leartech-evm-network and leartech-prysm, all az-only.
	var extra repeatable
	flag.Var(&extra, "deployed-root", "additional GitOps repo root to count as deployed (repeatable)")
	configPath := flag.String("renovate-config", "helmfiles/renovate/configs/renovate.yaml",
		"path, relative to -gitops-root, of the file holding the repositories list")
	exemptPath := flag.String("exempt", "helmfiles/renovate/enrolment-exempt.yaml",
		"path, relative to -gitops-root, of the exemption list")
	flag.Parse()

	seen := map[string]bool{}
	filesScanned := 0
	for _, r := range append([]string{*root}, extra...) {
		found, n, err := scanDeployed(filepath.Join(r, "helmfiles"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: cannot read helmfiles under %s: %v\n", r, err)
			os.Exit(1)
		}
		filesScanned += n
		for _, f := range found {
			seen[f] = true
		}
	}
	deployed := make([]string, 0, len(seen))
	for k := range seen {
		deployed = append(deployed, k)
	}
	sort.Strings(deployed)

	enrolled, err := scanEnrolled(filepath.Join(*root, *configPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: cannot read the repositories list at %s: %v\n", *configPath, err)
		os.Exit(1)
	}

	exempt, badExempt, err := scanExempt(filepath.Join(*root, *exemptPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: cannot read the exemption list at %s: %v\n", *exemptPath, err)
		os.Exit(1)
	}

	// The probe must prove it ran. Zero helmfiles or zero enrolled repos means
	// this check cannot see its own inputs, and a green tick would assert the
	// opposite of what it means. This guard is the reason the check is worth
	// having at all: the defect it hunts IS a silent absence.
	if filesScanned == 0 || len(deployed) == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: examined %d helmfile(s) and found %d deployed release(s).\n", filesScanned, len(deployed))
		fmt.Fprintln(os.Stderr, "      Nothing to compare against, so a pass here would be meaningless.")
		os.Exit(1)
	}
	if len(enrolled) == 0 {
		fmt.Fprintln(os.Stderr, "FAIL: the repositories list parsed to zero entries.")
		fmt.Fprintln(os.Stderr, "      Either the path is wrong or the format changed; both make this check blind.")
		os.Exit(1)
	}

	// HARD failures — a defect in the enrolment data itself. An exemption with no
	// reason is indistinguishable from the omission this check exists to catch, so
	// it stays a build-stopping FAIL.
	var hard []finding
	for _, r := range badExempt {
		hard = append(hard, finding{
			release: r,
			why:     "exempt from Renovate enrolment with no reason given",
			fix:     fmt.Sprintf("in %s, write `%s: <why this repo needs no dependency updates>`", *exemptPath, r),
		})
	}

	// SOFT findings — a deployed service not yet in Renovate's list. This is
	// EXPECTED transiently: a freshly bootstrapped service deploys before anything
	// enrols it, so failing the GitOps PR here blocks an unrelated promote for a
	// gap that will be closed (by the register action or the pr-stuck BA
	// shepherd). Report it loudly on the PR, but do NOT fail the build — missing
	// enrolment is a follow-up, not a reason to stop every unrelated change.
	//
	// proven-by: TestMain_UnenrolledDeployIsAWarningNotAFailure
	var soft []finding
	for _, rel := range deployed {
		if enrolled[rel] || exempt[rel] {
			continue
		}
		soft = append(soft, finding{
			release: rel,
			why:     "deployed to a cluster but absent from Renovate's repositories list, so it gets no dependency updates and no CVE alerts",
			fix:     fmt.Sprintf("add \"mikelear/%s\" to the repositories list in %s, or exempt it with a reason in %s", rel, *configPath, *exemptPath),
		})
	}

	fmt.Printf("==> renovate-enrolment: examined %d helmfile(s); %d deployed release(s) of ours, %d enrolled, %d exempt\n",
		filesScanned, len(deployed), len(enrolled), len(exempt))

	// The soft findings are WARNINGS: printed to stdout (so they land on the PR
	// comment), never exit-failing.
	if len(soft) > 0 {
		sort.Slice(soft, func(i, j int) bool { return soft[i].release < soft[j].release })
		fmt.Printf("\nWARNING: %d deployed repo(s) not yet in Renovate's scope (enrol to silence):\n\n", len(soft))
		for _, f := range soft {
			fmt.Printf("  %s\n      %s\n      fix: %s\n\n", f.release, f.why, f.fix)
		}
		fmt.Println("A repo Renovate never visits still looks configured but gets no CVE alerts.")
		fmt.Println("A freshly bootstrapped service lands here until it is enrolled — the register")
		fmt.Println("action or a pr-stuck BA shepherd closes the gap; this warning tracks it.")
	}

	// Only the HARD findings fail the build.
	if len(hard) == 0 {
		fmt.Println("==> renovate-enrolment: ok")
		return
	}
	sort.Slice(hard, func(i, j int) bool { return hard[i].release < hard[j].release })
	fmt.Fprintf(os.Stderr, "\nFAIL: %d exemption(s) without a reason:\n\n", len(hard))
	for _, f := range hard {
		fmt.Fprintf(os.Stderr, "  %s\n      %s\n      fix: %s\n\n", f.release, f.why, f.fix)
	}
	fmt.Fprintln(os.Stderr, "An exemption nobody justified is indistinguishable from the omission this")
	fmt.Fprintln(os.Stderr, "check exists to catch, so it must carry a reason.")
	os.Exit(1)
}

// scanDeployed returns the sorted set of release names that are ours, and the
// number of helmfile YAMLs read.
func scanDeployed(helmfilesDir string) ([]string, int, error) {
	seen := map[string]bool{}
	files := 0
	err := filepath.Walk(helmfilesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" && ext != ".gotmpl" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, m := range chartRef.FindAllStringSubmatch(string(b), -1) {
			ref := m[1]
			if !strings.HasPrefix(ref, oursChart) {
				continue
			}
			if name := strings.TrimPrefix(ref, oursChart); name != "" && !strings.Contains(name, "/") {
				seen[name] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, files, err
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, files, nil
}

func scanEnrolled(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range enrolledRef.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	return out, nil
}

// scanExempt reads `name: reason` lines. A name with an empty reason is
// returned separately so it can be reported as a finding rather than silently
// honoured: an exemption whose justification nobody wrote is indistinguishable
// from an omission, which is what this whole check exists to stop.
func scanExempt(path string) (map[string]bool, []string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil, nil
		}
		return nil, nil, err
	}
	out := map[string]bool{}
	var bad []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, reason, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if strings.TrimSpace(reason) == "" {
			bad = append(bad, name)
			continue
		}
		out[name] = true
	}
	sort.Strings(bad)
	return out, bad, nil
}
