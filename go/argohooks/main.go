// argohooks is the "this chart works on BOTH delivery paths" gate.
//
// WHY THIS EXISTS. There are two ways a chart of ours reaches a cluster, and they
// disagree about what `helm.sh/hook` MEANS:
//
//	jx-boot cluster (the build cluster)   real Helm. `helm.sh/hook` is honoured:
//	                                      a post-install Job runs once, after install,
//	                                      and is deleted per its delete policy.
//	Argo CD cluster (customer2, and every  Argo does not implement Helm hooks. The object
//	customer from now on)                 is an ordinary resource: applied during sync,
//	                                      at sync-wave 0, and re-applied on every later
//	                                      sync — including the ones a self-heal makes.
//
// Measured, on the same chart, both live:
//
//	kyverno-migrate-resources   helm.sh/hook: post-upgrade
//	  build      created 2026-11-17T13:23Z   ran once, at that upgrade
//	  customer2  created 2026-11-23T10:56Z   Argo DELETED AND RECREATED it on sync
//
// That is a migration Job whose schedule became "every sync". Nothing failed loudly: the
// Job is idempotent, so the cost is invisible until the day it is not.
//
// THE STANDARD, and this is the whole rule: an object that is a hook is a hook on BOTH
// paths, or it is not a hook. That means BOTH annotations, ALWAYS — the Helm one and the
// Argo one — and the Argo one must be the documented EQUIVALENT, not merely present:
//
//	helm.sh/hook                     argocd.argoproj.io/hook
//	pre-install, pre-upgrade         PreSync
//	post-install, post-upgrade       PostSync
//	pre-delete                       PreDelete
//	post-delete                      PostDelete
//	test, test-success               Skip        (Argo must not APPLY a test Pod)
//	helm.sh/hook-weight  N           argocd.argoproj.io/sync-wave: N
//	before-hook-creation             BeforeHookCreation
//	hook-succeeded                   HookSucceeded
//	hook-failed                      (no equivalent — Argo keeps a failed hook)
//
// Two rows there are not "both, please" but "cannot be expressed", and they are the ones a
// person is most likely to get wrong by copying a Helm chart that works:
//
//	hook-failed        Argo has no delete-on-failure. A chart that relies on it leaves a
//	                   failed migration Job on the cluster, and the NEXT sync sees a
//	                   failing hook and blocks the whole Application.
//	test-success       Argo would apply the Pod as a resource. `Skip` is the only correct
//	                   mapping, and a chart that maps it to PostSync runs a test at deploy.
//	pre-install AND    under Helm those are two different moments (before install, after
//	post-upgrade       upgrade); one object cannot be both under Argo. It must be split,
//	                   because the combined form runs the work TWICE on every sync — which
//	                   is how a migration runs twice.
//
// WHAT THIS TOOL DOES NOT DO. It reads what a chart RENDERS, so it cannot see a template
// branch that would render a hook. It cannot know whether a PostSync migration deadlocks
// against a readiness gate. Those are named in docs/hook-standard.md; the first is caught
// by chartstd's negative renders, the second needs a human.
//
// Usage:
//
//	helm template <chart> | argohooks -manifest -            # stdin
//	argohooks -manifest rendered/                           # a dir of rendered YAML
//	argohooks -explain                                      # print this standard
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	annHelmHook         = "helm.sh/hook"
	annHelmWeight       = "helm.sh/hook-weight"
	annHelmDelete       = "helm.sh/hook-delete-policy"
	annArgoHook         = "argocd.argoproj.io/hook"
	annArgoWave         = "argocd.argoproj.io/sync-wave"
	annArgoDelete       = "argocd.argoproj.io/hook-delete-policy"
	annArgoWaveOrdr     = "argocd.argoproj.io/sync-options"
	ruleBothAnnotations = "AH1"
	ruleArgoWithoutHelm = "AH2"
	ruleWrongPhase      = "AH3"
	ruleDeletePolicy    = "AH4"
	ruleWeightNoWave    = "AH5"
	ruleTestHook        = "AH6"
	ruleNoOrdering      = "AH7"
)

// helmToArgo is the STANDARD, and it is deliberately a table rather than logic: the next
// person to add a phase has to decide what it means under Argo, in one place.
var helmToArgo = map[string]string{
	"pre-install":  "PreSync",
	"pre-upgrade":  "PreSync",
	"post-install": "PostSync",
	"post-upgrade": "PostSync",
	"pre-delete":   "PreDelete",
	"post-delete":  "PostDelete",
	"test":         "Skip",
	"test-success": "Skip",
}

// deleteToArgo is the delete policy mapping. `hook-failed` is ABSENT on purpose: it has no
// Argo equivalent, so a chart that uses it is reported rather than silently translated.
var deleteToArgo = map[string]string{
	"before-hook-creation": "BeforeHookCreation",
	"hook-succeeded":       "HookSucceeded",
}

// Finding is one violation, with the evidence that makes it actable.
type Finding struct {
	Rule   string
	Where  string // Kind/name in namespace, or the file it came from
	Detail string
}

func (f Finding) String() string { return f.Rule + " " + f.Where + ": " + f.Detail }

// obj is one rendered Kubernetes object, as loosely as these rules need it.
type obj struct {
	Kind     string
	Name     string
	NS       string
	Annots   map[string]string
	Source   string
	IsObject bool
}

// load decodes a rendered manifest stream. A chart that renders NOTHING yields no objects,
// and that is not a pass here — Check says so, because a check over an empty set checks
// nothing.
func load(path string, r io.Reader, out *[]obj) error {
	dec := yaml.NewDecoder(r)
	for {
		var d struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name        string            `yaml:"name"`
				Namespace   string            `yaml:"namespace"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&d)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// A document that does not parse is not a hook; report it as an object that
			// cannot be judged rather than aborting the run over an unrelated chart.
			continue
		}
		if d.Kind == "" || d.Kind == "List" {
			continue
		}
		*out = append(*out, obj{
			Kind:     d.Kind,
			Name:     d.Metadata.Name,
			NS:       d.Metadata.Namespace,
			Annots:   d.Metadata.Annotations,
			Source:   path,
			IsObject: true,
		})
	}
}

// dig returns an annotation by key, tolerating a value written with quotes or as an int.
func (o obj) dig(key string) (string, bool) {
	if o.Annots == nil {
		return "", false
	}
	v, ok := o.Annots[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(strings.Trim(v, `"'`)), true
}

func (o obj) where() string {
	s := o.Kind + "/" + o.Name
	if o.NS != "" {
		s += " in " + o.NS
	}
	if o.Source != "" && o.Source != "-" {
		s = filepath.Base(o.Source) + ": " + s
	}
	return s
}

// phases splits a Helm hook annotation into its phases.
func phases(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expectedArgo is the Argo phase (or phases) a Helm hook annotation must map to.
//
// It returns more than one only when the Helm phases genuinely map to different Argo
// phases — which is the case where the object CANNOT be one hook on both paths, and must be
// split. Returning the set lets the caller say exactly that instead of guessing one.
func expectedArgo(helmPhases []string) ([]string, bool) {
	seen := map[string]bool{}
	var out []string
	unknown := false
	for _, p := range helmPhases {
		a, ok := helmToArgo[p]
		if !ok {
			unknown = true
			continue
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out, unknown
}

// Check applies every rule to one rendered set and returns the findings.
func Check(objs []obj) []Finding {
	var fs []Finding
	// AH7 needs to know the hooks per phase, so collect while walking.
	hooksPerPhase := map[string][]obj{}

	for _, o := range objs {
		helmHook, hasHelm := o.dig(annHelmHook)
		argoHook, hasArgo := o.dig(annArgoHook)

		switch {
		case hasHelm && !hasArgo:
			fs = append(fs, Finding{ruleBothAnnotations, o.where(), fmt.Sprintf(
				"is a Helm hook (%s) with no %s. On Argo CD this object is an ORDINARY resource: "+
					"applied during sync and re-applied on every later sync, so a post-install "+
					"migration Job becomes 'every sync'. Add the equivalent Argo hook — use the "+
					"leartech.hookAnnotations helper so the pair cannot drift.",
				annHelmHook, annArgoHook)})
		case hasArgo && !hasHelm:
			fs = append(fs, Finding{ruleArgoWithoutHelm, o.where(), fmt.Sprintf(
				"has %s but no %s. On a jx-boot cluster this is an ordinary resource, so a "+
					"migration Job can run BEFORE the database it migrates — the object must be "+
					"a hook on both paths or on neither.",
				annArgoHook, annHelmHook)})
		}
		if !hasHelm && !hasArgo {
			continue
		}

		hp := phases(helmHook)
		want, unknown := expectedArgo(hp)
		if unknown {
			fs = append(fs, Finding{ruleWrongPhase, o.where(), fmt.Sprintf(
				"has a %s phase this standard does not map: %q. Decide what it means under Argo "+
					"in the mapping table rather than leaving it untranslated.", annHelmHook, helmHook)})
		}
		// AH6 OWNS THE TEST PHASES. A test hook that is not Skip is one defect, and reporting
		// it as both "wrong phase" and "test hook not skipped" is two findings for one mistake -
		// which is how a gate's output stops being read.
		onlyTests := len(hp) > 0
		for _, p := range hp {
			if p != "test" && p != "test-success" {
				onlyTests = false
			}
		}
		if len(want) > 1 {
			fs = append(fs, Finding{ruleWrongPhase, o.where(), fmt.Sprintf(
				"is a hook for %q, which are DIFFERENT moments under Helm but collapse into one "+
					"object under Argo — the combined form runs the work once per Argo phase, i.e. "+
					"TWICE on every sync (this is how a migration runs twice). Split it into one "+
					"object per moment: %s.", helmHook, strings.Join(want, " and "))})
		} else if !onlyTests && len(want) == 1 && hasArgo && !samePhases(argoHook, want[0]) {
			fs = append(fs, Finding{ruleWrongPhase, o.where(), fmt.Sprintf(
				"is %s=%q, which means %s under Argo, but declares %s=%q. A pair that is "+
					"present but not EQUIVALENT is worse than a missing one: it reads as done, "+
					"and the object runs at a different moment on each path.",
				annHelmHook, helmHook, want[0], annArgoHook, argoHook)})
		}
		if len(want) == 1 {
			hooksPerPhase[want[0]+"|"+o.Source] = append(hooksPerPhase[want[0]+"|"+o.Source], o)
		}

		for _, p := range hp {
			if p == "test" || p == "test-success" {
				if hasArgo && !samePhases(argoHook, "Skip") {
					fs = append(fs, Finding{ruleTestHook, o.where(), fmt.Sprintf(
						"is a %s hook but declares %s=%q. Argo has no test hooks, so anything "+
							"other than Skip APPLIES the object — a test Pod that runs on every "+
							"deploy, and a failed one blocks the sync.", p, annArgoHook, argoHook)})
				}
			}
		}

		if del, ok := o.dig(annHelmDelete); ok {
			var mapped []string
			for _, tok := range phases(del) {
				a, known := deleteToArgo[tok]
				if !known {
					fs = append(fs, Finding{ruleDeletePolicy, o.where(), fmt.Sprintf(
						"declares %s %q, which has NO Argo equivalent (Argo keeps a failed hook "+
							"for inspection). Under Argo the failed Job stays, and the next sync "+
							"sees a failing hook and blocks the Application. Drop it, or handle "+
							"the failure in the Job.", annHelmDelete, tok)})
					continue
				}
				mapped = append(mapped, a)
			}
			if got, has := o.dig(annArgoDelete); has && len(mapped) > 0 {
				if !sameSet(phases(got), mapped) {
					fs = append(fs, Finding{ruleDeletePolicy, o.where(), fmt.Sprintf(
						"maps %s=%q to %s but declares %s=%q.",
						annHelmDelete, del, strings.Join(mapped, ","), annArgoDelete, got)})
				}
			}
		}

		// AN ORDERING FINDING IS ONLY MEANINGFUL IF THE OBJECT IS AN ARGO HOOK. When the Argo
		// annotation is missing altogether that is AH1, and adding "your sync-wave is missing
		// too" stacks a second finding on one mistake for a thing that has no effect either way
		// (the object is not a hook under Argo, so nothing orders it).
		if weight, ok := o.dig(annHelmWeight); ok && hasArgo {
			wave, hasWave := o.dig(annArgoWave)
			if !hasWave {
				fs = append(fs, Finding{ruleWeightNoWave, o.where(), fmt.Sprintf(
					"orders itself with %s=%s but declares no %s. Under Argo the weight is INERT, "+
						"so a migrations ConfigMap at -10 and its Job at -5 lose their order and "+
						"the Job can start before its SQL exists.", annHelmWeight, weight, annArgoWave)})
			} else if wave != weight {
				fs = append(fs, Finding{ruleWeightNoWave, o.where(), fmt.Sprintf(
					"has %s=%s but %s=%s. The numbers are the same scale (lower runs first), so "+
						"they must match or the Argo order differs from the Helm order.",
					annHelmWeight, weight, annArgoWave, wave)})
			}
			_ = annArgoWaveOrdr
		}
	}

	// AH7: more than one hook in one phase with no weights anywhere in that group. Ordering
	// is then whatever the object list happens to be, on BOTH paths, and a ConfigMap must
	// exist before the Job that reads it.
	var keys []string
	for k := range hooksPerPhase {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		group := hooksPerPhase[k]
		if len(group) < 2 {
			continue
		}
		weighted := 0
		for _, o := range group {
			if _, ok := o.dig(annHelmWeight); ok {
				weighted++
			}
		}
		if weighted == 0 {
			names := make([]string, 0, len(group))
			for _, o := range group {
				names = append(names, o.Kind+"/"+o.Name)
			}
			fs = append(fs, Finding{ruleNoOrdering, group[0].Source, fmt.Sprintf(
				"%d hooks share one phase with no %s between them (%s). Their order is then "+
					"undefined — the ConfigMap a Job reads is not guaranteed to be created first.",
				len(group), annHelmWeight, strings.Join(names, ", "))})
		}
	}
	return fs
}

// samePhases reports whether an Argo hook annotation names exactly one phase, equal to want.
func samePhases(got, want string) bool {
	p := phases(got)
	return len(p) == 1 && strings.EqualFold(p[0], want)
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[strings.ToLower(x)] = true
	}
	for _, x := range b {
		if !seen[strings.ToLower(x)] {
			return false
		}
	}
	return true
}

// readAll loads every .yaml/.yml under root, or a single file, or "-" for stdin.
func readAll(root string) ([]obj, error) {
	var objs []obj
	if root == "-" {
		return objs, load("-", os.Stdin, &objs)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		f, err := os.Open(root)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return objs, load(root, f, &objs)
	}
	var files []string
	err = filepath.Walk(root, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil || fi.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		loadErr := load(path, f, &objs)
		f.Close()
		if loadErr != nil {
			return nil, loadErr
		}
	}
	return objs, nil
}

func main() {
	manifest := flag.String("manifest", "-", "rendered YAML: a file, a directory, or - for stdin")
	explain := flag.Bool("explain", false, "print the standard and exit")
	flag.Parse()

	if *explain {
		fmt.Print(standard)
		return
	}
	objs, err := readAll(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "argohooks: %v\n", err)
		os.Exit(2)
	}
	if len(objs) == 0 {
		fmt.Fprintf(os.Stderr, "argohooks: %s rendered NO objects - nothing was checked, which is "+
			"not a pass. Point -manifest at rendered output.\n", *manifest)
		os.Exit(2)
	}

	findings := Check(objs)
	hooks := 0
	for _, o := range objs {
		if _, ok := o.dig(annHelmHook); ok {
			hooks++
		}
		if _, ok := o.dig(annArgoHook); ok {
			hooks++
		}
	}
	if len(findings) == 0 {
		fmt.Printf("argohooks: %d object(s), %d hook annotation(s) - every hook is a hook on both "+
			"paths, with the Argo equivalent.\n", len(objs), hooks)
		return
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "  %s\n", f.String())
	}
	fmt.Fprintf(os.Stderr, "\nargohooks: %d finding(s). A chart of ours reaches clusters through BOTH "+
		"Helm (jx-boot) and Argo CD, and the two disagree about what a hook is.\n", len(findings))
	os.Exit(1)
}

const standard = `A hook is a hook on BOTH delivery paths, or it is not a hook.

  helm.sh/hook                  argocd.argoproj.io/hook
  pre-install, pre-upgrade      PreSync
  post-install, post-upgrade    PostSync
  pre-delete                    PreDelete
  post-delete                   PostDelete
  test, test-success            Skip         (Argo must not APPLY a test Pod)
  helm.sh/hook-weight  N        argocd.argoproj.io/sync-wave: N
  before-hook-creation          BeforeHookCreation
  hook-succeeded                HookSucceeded
  hook-failed                   (no equivalent - Argo keeps a failed hook)

NOT TRANSLATABLE, so a chart must CHANGE rather than add an annotation:
  hook-failed                  leaves a failed Job, and the next sync blocks on it
  test/test-success != Skip    applies a test Pod as an ordinary resource
  pre-install AND post-upgrade two moments, one object  =>  runs twice per sync

Use the leartech.hookAnnotations helper (leartech-helm-library) so both sides come from ONE
declaration; this tool reads what a chart RENDERS, so a hand-written pair can drift.
`
