// scopedrift — a scope granted by the issuer that no service enforces, and a
// scope enforced by a service that no client can obtain.
//
// WHY THIS CANNOT BE A UNIT TEST. Both halves live in different repositories.
// leartech-auth-service decides which scopes each OAuth2 client is granted; a
// resource server decides which scopes its routes require. Neither repo can
// see the other, so each one's tests pass while the pair disagrees. That is
// the same shape as the Renovate enrolment gap: invisible from inside any
// single PR, and therefore months old before anyone noticed.
//
// THE TWO FAILURES, AND THEY ARE NOT SYMMETRIC.
//
//	GRANTED, ENFORCED BY NOTHING. leartech-auth-service's values.yaml already
//	documents four of these on one client: chat, embeddings, web_search and
//	web_fetch gate no route in leartech-ai-gateway. Its own comment names the
//	consequence precisely — such a scope is PRE-GRANTED, not merely
//	over-granted, because "the day a route gates one, every existing holder
//	gains that capability at once, and the PR that appears to add protection
//	is the one that switches access on. No diff shows the access change."
//
//	ENFORCED, GRANTABLE TO NOBODY. The mirror image, and it fails loudly
//	rather than silently: the route answers 403 to every caller forever. The
//	audience equivalent was measured on 2026-09-12 — nine audiences enforced
//	across the estate, thirteen mintable, and two enforced audiences that no
//	client could mint at all.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not check that a specific client
// holds a specific scope; that is a per-repo decision with a per-repo test
// (see TestBACliAudiencesAreAClosedSetWithMinimalScopePerService). This is
// only about the vocabulary agreeing at all.
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
	// A capability scope anywhere: a values file, a Go constant, a test
	// fixture. Deliberately broad on the way in; what matters is which SIDE
	// it was found on.
	scopeRe = regexp.MustCompile(`leartechapi:[a-z0-9_]+:[a-z0-9_]+`)

	// ENFORCED means reaching a RequireScope call, not appearing in the repo.
	//
	// The first version of this checker matched the scope string anywhere in a
	// Go file and reported "ok" against the estate — including the four
	// leartech-ai-gateway scopes that leartech-auth-service's own values.yaml
	// documents as gating nothing. A constant, a comment and a test fixture all
	// mention a scope; only a route enforces one.
	requireScopeRe = regexp.MustCompile(`RequireScope\(\s*([A-Za-z0-9_.]+|"[^"]+")\s*[,)]`)

	// const ScopeX auth.Scope = "leartechapi:a:b" — resolves the identifier a
	// RequireScope call names back to the scope it stands for.
	scopeConstRe = regexp.MustCompile(`(?m)^\s*(?:const\s+)?([A-Za-z0-9_]+)\s+auth\.Scope\s*=\s*"([^"]+)"`)

	goFile = regexp.MustCompile(`\.go$`)
)

type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	issuer := flag.String("issuer-root", "", "leartech-auth-service checkout (the repo that provisions scopes)")
	var services repeatable
	flag.Var(&services, "service-root", "a resource-server checkout that declares scopes (repeatable)")
	flag.Parse()

	if *issuer == "" || len(services) == 0 {
		fmt.Fprintln(os.Stderr, "usage: scopedrift -issuer-root <auth-service> -service-root <repo> [-service-root <repo>...]")
		os.Exit(64)
	}

	granted, grantedFiles := scanScopes(*issuer, func(p string) bool {
		// The issuer grants through chart values, not through Go.
		return strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")
	})

	declared := map[string][]string{} // scope -> services ENFORCING it
	declaredFiles := 0
	var opaque []string // service roots this checker cannot read
	for _, s := range services {
		found, n := scanEnforced(s)
		declaredFiles += n
		name := filepath.Base(strings.TrimRight(s, "/"))
		if len(found) == 0 {
			// Zero enforced scopes is not "this service enforces nothing" — it
			// is "this checker could not tell". It understands go-common's
			// verifier.RequireScope with `auth.Scope` constants and nothing
			// else, so a repo with its own middleware.RequireScope, or its own
			// scope type, reads as empty. leartech-artifact-api and
			// leartech-ai-gateway are both in that position today and both
			// genuinely gate routes.
			//
			// Reporting those scopes as pre-granted would flag correct
			// configuration, which is how a check gets switched off.
			opaque = append(opaque, name)
			continue
		}
		for sc := range found {
			declared[sc] = append(declared[sc], name)
		}
	}

	// A probe that read nothing is not a probe that found nothing. Both sides
	// must have been readable, or every conclusion below is about an empty set.
	if grantedFiles == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: read 0 YAML files under %s; the issuer side is unreadable.\n", *issuer)
		os.Exit(2)
	}
	if declaredFiles == 0 {
		fmt.Fprintf(os.Stderr, "FAIL: read 0 non-test Go files across %v; the service side is unreadable.\n", []string(services))
		os.Exit(2)
	}

	// Only scopes belonging to a service this checker COULD read are
	// assessable. A scope whose prefix names an opaque service is unknown, not
	// pre-granted.
	assessable := func(scope string) bool {
		for _, name := range opaque {
			// leartechapi:gateway:chat -> "gateway"; match it against the repo
			// name so leartech-ai-gateway claims the gateway vocabulary.
			parts := strings.Split(scope, ":")
			if len(parts) >= 2 && strings.Contains(name, parts[1]) {
				return false
			}
		}
		return true
	}

	var preGranted, unmintable, unknown []string
	for sc := range granted {
		if _, ok := declared[sc]; ok {
			continue
		}
		if assessable(sc) {
			preGranted = append(preGranted, sc)
		} else {
			unknown = append(unknown, sc)
		}
	}
	sort.Strings(unknown)
	for sc := range declared {
		if _, ok := granted[sc]; !ok {
			unmintable = append(unmintable, sc)
		}
	}
	sort.Strings(preGranted)
	sort.Strings(unmintable)

	fmt.Printf("==> scope-drift: %d granted by the issuer, %d ENFORCED across %d service(s)\n",
		len(granted), len(declared), len(services))
	if len(opaque) > 0 {
		fmt.Printf("==> not assessable (no go-common RequireScope found): %s\n", strings.Join(opaque, ", "))
		fmt.Printf("    %d scope(s) skipped because the service that would enforce them\n", len(unknown))
		fmt.Printf("    could not be read. Adopting verifier.RequireScope brings them in.\n")
	}

	if len(preGranted) == 0 && len(unmintable) == 0 {
		fmt.Println("==> scope-drift: ok — every granted scope is enforced, every enforced scope is grantable")
		return
	}

	if len(preGranted) > 0 {
		fmt.Fprintf(os.Stderr, "\nGRANTED, ENFORCED BY NOTHING (%d):\n\n", len(preGranted))
		for _, sc := range preGranted {
			fmt.Fprintf(os.Stderr, "  %s\n", sc)
		}
		fmt.Fprintln(os.Stderr, "\n  These are PRE-GRANTED. The day a route gates one, every existing holder")
		fmt.Fprintln(os.Stderr, "  gains that capability at once, and the PR that appears to add protection")
		fmt.Fprintln(os.Stderr, "  is the one that switches access on. No diff shows the access change.")
		fmt.Fprintln(os.Stderr, "  Either gate them, or stop granting them.")
	}

	if len(unmintable) > 0 {
		fmt.Fprintf(os.Stderr, "\nENFORCED, GRANTABLE TO NOBODY (%d):\n\n", len(unmintable))
		for _, sc := range unmintable {
			fmt.Fprintf(os.Stderr, "  %s  (enforced by %s)\n", sc, strings.Join(declared[sc], ", "))
		}
		fmt.Fprintln(os.Stderr, "\n  A route requires these and no client can obtain them, so it answers 403")
		fmt.Fprintln(os.Stderr, "  to every caller forever. Grant them to the clients that need them.")
	}
	os.Exit(1)
}

// scanEnforced returns the scopes a repo actually gates a route on: those
// reaching a RequireScope call, with identifiers resolved through the
// `auth.Scope` constants that name them.
func scanEnforced(root string) (map[string]bool, int) {
	consts := map[string]string{} // identifier -> scope string
	var calls []string            // identifiers or literals passed to RequireScope
	files := 0

	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !goFile.MatchString(p) || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files++
		src := string(b)
		for _, m := range scopeConstRe.FindAllStringSubmatch(src, -1) {
			consts[m[1]] = m[2]
		}
		for _, m := range requireScopeRe.FindAllStringSubmatch(src, -1) {
			calls = append(calls, m[1])
		}
		return nil
	})

	out := map[string]bool{}
	for _, c := range calls {
		if strings.HasPrefix(c, `"`) {
			out[strings.Trim(c, `"`)] = true
			continue
		}
		// pkg.Ident or Ident — the constant is keyed on the bare identifier.
		if i := strings.LastIndex(c, "."); i >= 0 {
			c = c[i+1:]
		}
		if sc, ok := consts[c]; ok {
			out[sc] = true
		}
	}
	return out, files
}

// scanScopes walks root and returns the scope vocabulary found in files the
// predicate accepts, plus how many such files were read.
func scanScopes(root string, want func(string) bool) (map[string]bool, int) {
	out := map[string]bool{}
	files := 0
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				switch info.Name() {
				case ".git", "vendor", "node_modules":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !want(p) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		files++
		for _, m := range scopeRe.FindAllString(string(b), -1) {
			out[m] = true
		}
		return nil
	})
	return out, files
}
