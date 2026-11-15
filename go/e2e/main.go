// Package main holds the e2e tooling: one binary, two subcommands.
//
//	e2e contract  — static checks on a repo's declaration and pack. No cluster.
//	e2e wait      — wait for a preview, guided by that same declaration.
//
// WHY ONE BINARY. The declaration is read by both, and reading it twice is how
// the estate's current readiness logic came to exist in four places that
// disagree: the same fact, parsed or probed slightly differently per consumer.
// One parser, one set of defaults, one opinion about what a missing key means.
//
// The subcommands are named after what a consumer does with them, not after the
// repo's folder layout, so `e2e contract` reads as "check my declaration" and
// `e2e wait` as "wait for my preview".
package main

import (
	"fmt"
	"os"
)

const usage = `e2e — the end2end declaration and the preview it describes.

  e2e contract -dir <repo> [-check-name=web|ui] [-strict]
        Static checks on end2end/preview.yaml and the pack beside it. No cluster,
        no network. Exits 1 on a finding, so it is a presubmit.

  e2e wait -dir <repo> [-namespace <ns>] [-version <sha>] [-mode web|ui]
        Wait for the preview the declaration describes, and print one verdict.
        Prints the reason it is not ready, naming the deployment, URL and status.

  e2e rehearse -dir <repo> -namespace <ns>
        The same logic as wait, against a preview that already exists, printing
        every step. This is what a developer runs instead of opening a PR.

  e2e explain -dir <repo>
        Print the parsed declaration — defaults resolved — so a consumer can see
        what the pipeline will actually do. A declaration you cannot read back is
        one you cannot review.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(64)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	switch sub {
	case "contract":
		os.Exit(runContract(args))
	case "wait":
		os.Exit(runWait(args, false))
	case "rehearse":
		os.Exit(runWait(args, true))
	case "explain":
		os.Exit(runExplain(args))
	case "-h", "--help", "help":
		fmt.Print(usage)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "e2e: unknown subcommand %q\n\n%s", sub, usage)
		os.Exit(64)
	}
}
