// authcoverage derives the estate's auth test coverage from the tests
// themselves and FAILS when a repo cannot prove what it declares.
//
// It is not a document. Every column is read from files that break when the
// code moves: .authprofile, *_test.go function names, end2end/*.sh, and the
// preview helmfile. Nothing here is written by hand, so nothing here can be
// out of date while looking current — which is the failure this exists to end.
//
// The motivating incident: on 2026-09-13 a session was told "nothing in the
// estate enforces scopes". That was false — leartech-artifact-api gates every
// route on a read or write scope and proves it in unit tests, including
// TestRFC6749_3_3_ReadScopeDoesNotGrantWrite. The claim came from reading code
// and a README rather than the tests. Running this would have answered it in
// one line.
//
// Usage:
//
//	authcoverage <repo-dir> [repo-dir...]     # markdown matrix to stdout
//	authcoverage -strict <repo-dir>...        # also exit 1 on an unproven claim
//
// STRICT MODE is the point. A repo that declares an auth role and has unit
// proof but NO deployed proof is a gap that used to be invisible; here it is a
// non-zero exit.
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

// category groups auth test names by the property they prove. Patterns match
// TEST NAMES, not bodies: a name is the one part of a test written to be read.
type category struct {
	name string
	re   *regexp.Regexp
}

var categories = []category{
	{"audience", regexp.MustCompile(`(?i)audience|RFC8707|Aud\b`)},
	{"scope", regexp.MustCompile(`(?i)scope|RFC6749`)},
	{"issuer/signature", regexp.MustCompile(`(?i)issuer|RFC7519|RFC7515|JWKS|Signature|Expired`)},
	{"DCR", regexp.MustCompile(`(?i)DCR|RFC7591|RFC9728|DynamicClient`)},
}

var (
	testFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)`)
	// A deployed suite proves auth only if it obtains or presents a token.
	// Listing scripts is not evidence; artifact-api had three that touched
	// none of this and its own comment said it could not.
	mintsRe  = regexp.MustCompile(`oauth2/token|/oauth2/register`)
	bearerRe = regexp.MustCompile(`Authorization:\s*Bearer|Bearer \$`)
	umbrella = "leartech-preview-infrastructure"
)

type repo struct {
	Name        string
	Profile     string
	UnitByCat   map[string][]string
	E2EScripts  int
	E2EAuth     []string // scripts that mint or present a token
	HasUmbrella bool
	OwnIssuer   bool // deploys Hydra itself, so it needs no umbrella
	Err         string

	goTestFiles int // anchor: proves the walk actually read something
}

func main() {
	strict := flag.Bool("strict", false, "exit 1 when a repo declares an auth role it cannot prove deployed")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: authcoverage [-strict] <repo-dir>...")
		os.Exit(2)
	}

	var repos []repo
	for _, dir := range flag.Args() {
		repos = append(repos, scan(dir))
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

	printMatrix(repos)

	gaps := unproven(repos)
	if len(gaps) > 0 {
		fmt.Printf("\n## Declared but not proven deployed\n\n")
		for _, g := range gaps {
			fmt.Printf("- **%s** — declares `%s`, has %d auth unit test(s), and no end2end that obtains or presents a token%s\n",
				g.Name, g.Profile, g.unitTotal(), umbrellaNote(g))
		}
		if *strict {
			fmt.Fprintf(os.Stderr, "\nFAIL: %d repo(s) declare an auth role they cannot prove in a deployed pod.\n", len(gaps))
			fmt.Fprintln(os.Stderr, "A unit suite proves the CODE refuses correctly. It cannot prove the running")
			fmt.Fprintln(os.Stderr, "pod is wired to the issuer and audience it asserts against — which is where")
			fmt.Fprintln(os.Stderr, "every auth failure in this estate has actually been.")
			os.Exit(1)
		}
	}
}

// unproven: declares an auth role, has unit coverage, but nothing deployed.
//
// Requiring unit coverage before reporting a gap is deliberate. A repo with
// neither has not started; a repo with unit tests and no deployed proof has
// stopped one step short, and that step is the one that catches wiring.
func unproven(repos []repo) []repo {
	var out []repo
	for _, r := range repos {
		if r.Profile == "" || r.Profile == "none" || r.Err != "" {
			continue
		}
		if r.unitTotal() > 0 && len(r.E2EAuth) == 0 {
			out = append(out, r)
		}
	}
	return out
}

func umbrellaNote(r repo) string {
	if r.OwnIssuer {
		return ""
	}
	if !r.HasUmbrella {
		return ", and its preview does not deploy " + umbrella + " so it could not mint one"
	}
	return ""
}

func (r repo) unitTotal() int {
	n := 0
	for _, v := range r.UnitByCat {
		n += len(v)
	}
	return n
}

func scan(dir string) repo {
	r := repo{Name: filepath.Base(dir), UnitByCat: map[string][]string{}}
	if _, err := os.Stat(dir); err != nil {
		r.Err = "unreadable"
		return r
	}

	// Resolve symlinks before walking. filepath.WalkDir does NOT follow them,
	// so a symlinked repo produced a row of zeros — reporting "no coverage"
	// for a repo it never actually read. Distinguishing "looked and found
	// none" from "could not look" is the whole job of this tool; a silent zero
	// here would be the same defect it exists to expose.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	if b, err := os.ReadFile(filepath.Join(dir, ".authprofile")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "type:") {
				r.Profile = strings.TrimSpace(strings.TrimPrefix(line, "type:"))
				break
			}
		}
	}

	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(p, "_test.go"):
			r.goTestFiles++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			for _, m := range testFuncRe.FindAllStringSubmatch(string(b), -1) {
				for _, c := range categories {
					if c.re.MatchString(m[1]) {
						r.UnitByCat[c.name] = append(r.UnitByCat[c.name], m[1])
					}
				}
			}
		case strings.Contains(p, "end2end") && strings.HasSuffix(p, ".sh"):
			if filepath.Base(p) == "run.sh" {
				return nil // the runner, not a scenario
			}
			r.E2EScripts++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			if mintsRe.Match(b) || bearerRe.Match(b) {
				r.E2EAuth = append(r.E2EAuth, filepath.Base(p))
			}
		case filepath.Base(p) == "Chart.yaml":
			// A repo whose own chart depends on hydra IS an issuer deployment —
			// leartech-auth-service does, unconditionally. Reporting
			// "umbrella in preview: no" for it is a false negative on the row
			// anyone reads first: it needs no umbrella because it brings the
			// issuer with it.
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), "name: hydra") {
				r.OwnIssuer = true
			}
		case strings.HasPrefix(filepath.Base(p), "helmfile") && strings.Contains(p, "preview"):
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), umbrella) {
				r.HasUmbrella = true
			}
		}
		return nil
	})

	// Anchor. A Go repo with no _test.go files at all means the walk failed or
	// the path is not what the caller thinks, not that the repo has no tests.
	// Saying "no auth coverage" on that basis would be a confident wrong
	// answer, which is worse than an error.
	if r.goTestFiles == 0 {
		r.Err = "no _test.go found — wrong path, or not a Go repo"
	}
	return r
}

func printMatrix(repos []repo) {
	fmt.Println("## Auth coverage, derived from tests")
	fmt.Println()
	fmt.Println("| repo | declares | audience | scope | issuer/sig | DCR | deployed auth proof | umbrella in preview |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, r := range repos {
		if r.Err != "" {
			fmt.Printf("| `%s` | — | | | | | _%s_ | |\n", r.Name, r.Err)
			continue
		}
		cells := make([]string, 0, len(categories))
		for _, c := range categories {
			n := len(r.UnitByCat[c.name])
			if n == 0 {
				cells = append(cells, "—")
			} else {
				cells = append(cells, fmt.Sprintf("%d", n))
			}
		}
		deployed := "**none**"
		if len(r.E2EAuth) > 0 {
			deployed = strings.Join(r.E2EAuth, ", ")
		}
		umb := "no"
		switch {
		case r.HasUmbrella:
			umb = "yes"
		case r.OwnIssuer:
			umb = "n/a — own issuer"
		}
		prof := r.Profile
		if prof == "" {
			prof = "_undeclared_"
		}
		fmt.Printf("| `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
			r.Name, prof, cells[0], cells[1], cells[2], cells[3], deployed, umb)
	}
	fmt.Println()
	fmt.Println("Counts are auth-related **test names**, not assertions — a name is the part of")
	fmt.Println("a test written to be read. `deployed auth proof` lists end2end scripts that")
	fmt.Println("actually obtain or present a token; a suite that exercises no token proves")
	fmt.Println("nothing about auth however many scripts it has.")
}
