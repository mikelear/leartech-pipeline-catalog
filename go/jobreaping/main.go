// jobreaping — a Job that never expires is a leak with a green tick.
//
// WHY THIS EXISTS. On 2026-09-14 the two build clusters held 12,046 Jobs
// between them. 10,674 were jx-verify-gc-jobs, the oldest 244 days old. The
// garbage-collection job was the garbage. Purging them dropped gcp from 7,000
// Jobs to 750 and az from 5,053 to 622.
//
// Nothing was broken in a way anyone could see. Jobs are cheap individually,
// they just never leave, and the cost lands on etcd and on every `kubectl get`
// long before it lands on a dashboard.
//
// THREE SURFACES, BECAUSE THE WORST OFFENDER IS NOT IN A CHART.
//
//  1. Chart templates rendering `kind: Job`. A plain Job needs
//     ttlSecondsAfterFinished; nothing else reaps it.
//
//  2. Chart templates rendering `kind: CronJob`. These are ALREADY bounded by
//     successfulJobsHistoryLimit / failedJobsHistoryLimit, which genuinely
//     work. Requiring a TTL as well would flag correctly-configured charts, so
//     either mechanism satisfies this check. 15 of the estate's 26 Job
//     templates were already compliant when this was written -- a check that
//     failed them would have been wrong, not strict.
//
//  3. GO CODE constructing batchv1.Job. This is the surface that matters most
//     and the one a template-only check misses entirely. leartech-arrivals-
//     observer dispatches a forensics Job per arrival from
//     internal/dispatch/dispatch.go with BackoffLimit and
//     ActiveDeadlineSeconds set and no TTLSecondsAfterFinished. Result: 205
//     completed forensics pods on az against 11 on gcp, owned by nothing,
//     annotated by no helm release, invisible to any chart audit.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not demand a particular TTL value.
// A minute and a week are both defensible depending on whether anyone reads
// the logs; zero-and-unbounded is the only indefensible answer.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	kindJob     = regexp.MustCompile(`(?m)^\s*kind:\s*Job\s*$`)
	kindCronJob = regexp.MustCompile(`(?m)^\s*kind:\s*CronJob\s*$`)
	ttlYAML     = regexp.MustCompile(`ttlSecondsAfterFinished`)
	histYAML    = regexp.MustCompile(`successfulJobsHistoryLimit|failedJobsHistoryLimit`)

	// Go: a composite literal for a Job or its spec. Matches batchv1.Job{,
	// batchv1.JobSpec{ and the dot-imported forms.
	goJobLit = regexp.MustCompile(`\b(batchv1|batch)\.Job(Spec)?\{`)
	ttlGo    = regexp.MustCompile(`TTLSecondsAfterFinished`)
)

type finding struct{ file, why, fix string }

func main() {
	root := flag.String("root", ".", "repo root to scan")
	flag.Parse()

	var findings []finding
	var scannedYAML, scannedGo int

	_ = filepath.Walk(*root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			if fi != nil && fi.IsDir() {
				switch fi.Name() {
				case ".git", "node_modules", "vendor", "versionStream", "config-root":
					return filepath.SkipDir
				case "testdata":
					// A Go convention the toolchain already ignores, and where
					// THIS checker keeps its own deliberately-broken fixtures.
					// Named, not a category: "examples" or "fixtures" are not
					// exempt, because an exemption that grows is how a gate
					// stops meaning anything. Scanning a directory explicitly
					// passed as --root still works -- only a testdata dir
					// found while walking is skipped.
					if p != *root {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		s := string(b)
		rel, _ := filepath.Rel(*root, p)

		switch {
		case strings.HasSuffix(p, ".yaml"), strings.HasSuffix(p, ".yml"):
			if !strings.Contains(p, string(filepath.Separator)+"templates"+string(filepath.Separator)) {
				return nil
			}
			scannedYAML++
			if kindCronJob.MatchString(s) {
				// Either mechanism is fine: history limits genuinely bound a
				// CronJob's Jobs, and a TTL on the jobTemplate does too.
				if !histYAML.MatchString(s) && !ttlYAML.MatchString(s) {
					findings = append(findings, finding{rel,
						"CronJob with neither successfulJobsHistoryLimit/failedJobsHistoryLimit nor ttlSecondsAfterFinished",
						"set successfulJobsHistoryLimit and failedJobsHistoryLimit, or ttlSecondsAfterFinished on the jobTemplate"})
				}
			} else if kindJob.MatchString(s) {
				if !ttlYAML.MatchString(s) {
					findings = append(findings, finding{rel,
						"plain Job with no ttlSecondsAfterFinished",
						"add spec.ttlSecondsAfterFinished — nothing else reaps a standalone Job"})
				}
			}
		case strings.HasSuffix(p, ".go"):
			if strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scannedGo++
			if goJobLit.MatchString(s) && !ttlGo.MatchString(s) {
				findings = append(findings, finding{rel,
					"constructs a batchv1.Job with no TTLSecondsAfterFinished",
					"set TTLSecondsAfterFinished on the JobSpec — a Job created by a service is owned by nothing and reaped by nothing"})
			}
		}
		return nil
	})

	// A scan that examined nothing is not a scan that found nothing. Without
	// this, a wrong --root or a repo with no charts reports a clean pass.
	if scannedYAML == 0 && scannedGo == 0 {
		fmt.Fprintln(os.Stderr, "FAIL: job-reaping examined 0 template YAML files and 0 Go files.")
		fmt.Fprintln(os.Stderr, "      A green tick here would mean the opposite of what it says.")
		os.Exit(1)
	}

	fmt.Printf("==> job-reaping: examined %d chart template(s) and %d Go file(s)\n", scannedYAML, scannedGo)

	if len(findings) == 0 {
		fmt.Println("==> job-reaping: ok")
		return
	}

	fmt.Fprintf(os.Stderr, "\nFAIL: %d Job source(s) with nothing to reap them:\n\n", len(findings))
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "  %s\n      %s\n      fix: %s\n\n", f.file, f.why, f.fix)
	}
	fmt.Fprintln(os.Stderr, "A Job that never expires is a leak with a green tick. On 2026-09-14 the")
	fmt.Fprintln(os.Stderr, "two build clusters held 12,046 Jobs; 10,674 of them were one un-reaped")
	fmt.Fprintln(os.Stderr, "source, the oldest 244 days old, and nothing had ever reported a problem.")
	os.Exit(1)
}
