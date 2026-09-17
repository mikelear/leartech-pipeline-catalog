// e2eskipcontract fails a PR whose end2end scripts and runner disagree about
// what exit 77 means.
//
// THE BUG THIS EXISTS FOR. The same e2e scripts are executed by TWO runners:
//
//	PR      tasks/end2end/pullrequest.yaml INLINES its own runner. It has
//	        honoured SKIP_EXIT=77 since it was written, and counts exit 0 as
//	        a PASS.
//	Staging arrivals-observer dispatches the pack and the REPO'S OWN
//	        end2end/run.sh executes it.
//
// A repo that teaches one and not the other gets a green PR and a red
// staging gate, or the reverse.
//
// Measured 2026-09-17 on leartech-auth-service. PR 209 changed 16 skip
// branches from `exit 0` to `exit 77` so the PR report would stop counting a
// skip as a pass. Its own run.sh had never heard of 77, so in staging every
// honest skip became a failure:
//
//	"status": "fail"
//	"message": "[smoke] preview-only checks — skipping in staging"
//	==> results.json.success=false — overriding TEST_EXIT 0 -> 1
//
// Versions 0.1.130 and 0.1.131 set Arrival.phase=Failed and turned
// leartech-gate red on the GitOps PRs of BOTH clusters, for a service that
// was fine. Every check on the PR that introduced it was green, because the
// PR lane was the half that had been taught.
//
// WHY THIS IS A REPO-LOCAL CHECK. It would be possible to ask whether the
// service has testPacks configured in the Observer and stay quiet if staging
// never runs the pack. That is deliberately NOT done here: testPacks are
// configuration that changes without touching this repo, so a check keyed on
// them would pass today and the latent mismatch would surface the day someone
// enables a pack — which is the worst possible moment to discover it. The two
// files live in one repo and either agree or do not.
//
// Exit codes
//
//	0   the two agree, or there is nothing that could disagree
//	1   mismatch: scripts exit 77, the runner does not understand it
//	2   nothing readable — a probe that examined no scripts is not a pass
//	64  the caller passed something unusable
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// scriptSkips matches a literal `exit 77`. A script signalling a skip any
// other way is not this checker's business.
var scriptSkips = regexp.MustCompile(`(?m)^\s*exit\s+77\b`)

// runnerHonours matches the ways a runner can be seen to treat 77 specially.
// Deliberately narrow: a bare `77` anywhere would match a timeout or a port
// and report a runner as safe when it is not. Each alternative is a form seen
// in a real runner in this estate.
var runnerHonours = regexp.MustCompile(
	`SKIP_EXIT\s*=\s*77` + // SKIP_EXIT=77
		`|-eq\s+77` + // [ "$rc" -eq 77 ]
		`|-eq\s+"?\$\{?SKIP_EXIT` + // [ "$rc" -eq "$SKIP_EXIT" ]
		`|==\s*77`, // (( rc == 77 ))
)

func main() {
	dir := flag.String("dir", "end2end", "directory holding the e2e scripts and run.sh")
	flag.Parse()

	if strings.TrimSpace(*dir) == "" {
		fmt.Fprintln(os.Stderr, "FAIL: -dir is empty; nothing to check.")
		os.Exit(64)
	}

	if fi, err := os.Stat(*dir); err != nil || !fi.IsDir() {
		// Not every repo has an e2e suite. That is not a failure, but say so:
		// a checker that prints nothing is indistinguishable from one that did
		// not run.
		fmt.Printf("==> e2eskipcontract: no %s directory; nothing to check.\n", *dir)
		os.Exit(0)
	}

	// EVERY .sh, not just the numbered ones. Scoping this to
	// [0-9][0-9]-*.sh made the check blind to a suite that names its files
	// anything else — and a renamed suite is exactly when a contract breaks
	// unnoticed. run.sh is excluded because it is the runner, not a check.
	var paths []string
	walkErr := filepath.WalkDir(*dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sh") || filepath.Base(p) == "run.sh" {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "FAIL: unable to walk %s: %v\n", *dir, walkErr)
		os.Exit(2)
	}

	var using77 []string
	examined := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: cannot read %s: %v\n", p, err)
			os.Exit(2)
		}
		examined++
		if scriptSkips.Match(b) {
			using77 = append(using77, filepath.Base(p))
		}
	}

	// AN EMPTY SUITE IS NOT A FAILURE, and an earlier version of this checker
	// got that wrong. It exited 2 on any end2end/ holding no numbered script,
	// which would have failed the next PR in four repos that simply structure
	// their suite differently: leartech-helm-library (render_test.sh and a
	// fixture directory), leartech-infra-agent (a run.sh and nothing yet),
	// leartech-orchestrator-controller and leartech-sc-event-listener
	// (placeholders).
	//
	// The guard was aimed at the right risk and pointed at the wrong thing.
	// Without a script that exits 77 there is no contract to violate, so
	// finding none is a real answer rather than a broken probe. The blindness
	// it was worried about is now handled by walking every .sh instead of one
	// naming convention.
	if examined == 0 {
		fmt.Printf("==> e2eskipcontract: %s holds no shell checks; nothing can disagree.\n", *dir)
		os.Exit(0)
	}

	runner := filepath.Join(*dir, "run.sh")
	rb, rerr := os.ReadFile(runner)
	if rerr != nil {
		// No run.sh means arrivals-observer has no runner to dispatch in
		// staging, so the two lanes cannot disagree.
		fmt.Printf("==> e2eskipcontract: examined %d script(s); no %s, so staging runs no pack here.\n",
			examined, runner)
		os.Exit(0)
	}

	honours := runnerHonours.Match(rb)
	sort.Strings(using77)

	switch {
	case len(using77) > 0 && !honours:
		fmt.Fprintf(os.Stderr,
			"FAIL: %d script(s) exit 77 to signal a skip, but %s does not treat 77 specially.\n\n"+
				"  %s\n\n"+
				"The PR pipeline will look green: the catalog's end2end task inlines its own\n"+
				"runner, which honours SKIP_EXIT=77. Staging runs THIS file, which will record\n"+
				"each of those skips as a FAILURE, set Arrival.phase=Failed and turn the\n"+
				"leartech-gate red on every GitOps PR until the version is superseded.\n\n"+
				"That is not hypothetical: leartech-auth-service 0.1.130 and 0.1.131 did exactly\n"+
				"this on both clusters on 2026-09-17.\n\n"+
				"Teach the runner the convention — see leartech-maestro-service/end2end/run.sh\n"+
				"or leartech-auth-service/end2end/run.sh:\n\n"+
				"  SKIP_EXIT=77\n"+
				"  rc_script=0\n"+
				"  bash \"$script\" >\"$log\" 2>&1 || rc_script=$?\n"+
				"  if   [ \"$rc_script\" -eq 0 ];           then status=\"pass\"\n"+
				"  elif [ \"$rc_script\" -eq \"$SKIP_EXIT\" ]; then status=\"skip\"\n"+
				"  else                                    status=\"fail\"\n"+
				"  fi\n",
			len(using77), runner, strings.Join(using77, "\n  "))
		os.Exit(1)

	case len(using77) > 0:
		fmt.Printf("==> e2eskipcontract: %d of %d script(s) exit 77 and %s honours it.\n",
			len(using77), examined, runner)

	default:
		fmt.Printf("==> e2eskipcontract: examined %d script(s); none exits 77. %s %s.\n",
			examined, runner,
			map[bool]string{true: "honours 77 already", false: "does not handle 77, which nothing needs yet"}[honours])
	}
	os.Exit(0)
}
