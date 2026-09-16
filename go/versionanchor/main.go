// versionanchor — which build actually replied.
//
// WHY THIS EXISTS. A preview can be genuinely healthy and genuinely answering
// while serving the PREVIOUS commit: a stale chart, or an image tag left
// pinned to an earlier build, and rollout is green on the wrong version. Every
// check downstream then certifies the previous commit in the most convincing
// way available. Across 8 repos and 36 numbered end2end scripts, not one
// asserted which build replied.
//
// The anchor is a handful of lines of shell in tasks/end2end/pullrequest.yaml,
// and it needed FOUR corrections in a single day. Every one was a pure
// input-to-decision bug — the kind a table test settles permanently and a
// pipeline re-run diagnoses one preview at a time:
//
//  1. Anchored on $VERSION, which numbers the PIPELINERUN rather than the
//     deploy, so a partial `/retest` failed a preview serving exactly the right
//     commit. The deploy's intent comes from preview-gate instead.
//
//  2. Substring comparison. VERSION=0.0.0-PR-7-3 matched a pod running
//     0.0.0-PR-7-30 — iteration 3 accepted as iteration 30. Reachable on any PR
//     retested into double digits, and it certifies the wrong build green.
//
//  3. ${img##*:} to take the tag. jx pins images as name:tag@sha256:digest, so
//     everything-after-the-last-colon is the digest hex and never the tag.
//     Every digest-pinned preview then failed while serving the right build,
//     reporting "no running image carries VERSION=X" about an image tagged X.
//
//  4. A bare VAR=$(curl -fsS ...) under `set -eo pipefail`. curl -f exits 22 on
//     an HTTP error and the failed assignment took the step with it, before the
//     "cannot anchor" fallback was reached. Observed on auth-service PR #193's
//     az preview: 19 of 19 checks passed and the step still exited 22.
//
// WHAT IT DELIBERATELY DOES NOT DO. It does not decide whether an unreadable
// gate is fatal — that is the caller's policy, and conflating "cannot anchor"
// with "wrong version" is how (4) happened. `expected` exits 3 when the field
// is absent so the caller can tell those apart, and `match` exits 2 when it was
// handed no images at all, because a probe that inspected nothing is not a
// probe that found nothing.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(64)
	}
	switch os.Args[1] {
	case "expected":
		os.Exit(cmdExpected(os.Stdin, os.Stdout))
	case "match":
		fs := flag.NewFlagSet("match", flag.ExitOnError)
		want := fs.String("expected", "", "the version the deploy intended")
		_ = fs.Parse(os.Args[2:])
		os.Exit(cmdMatch(os.Stdin, os.Stdout, *want))
	case "tag":
		if len(os.Args) != 3 {
			usage()
			os.Exit(64)
		}
		fmt.Println(imageTag(os.Args[2]))
		return
	default:
		usage()
		os.Exit(64)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  versionanchor expected            < gate-body.json   # print expected_version (exit 3 if absent)
  versionanchor match -expected=V   < images.txt       # exit 0 matched, 1 no match, 2 no images
  versionanchor tag <image-ref>                        # print the tag, digest stripped`)
}

// cmdExpected reads preview-gate's JSON body and prints expected_version.
//
// A real parser, not a regex. The shell used
//
//	sed -n 's/.*"expected_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
//
// which is correct for the body the gate emits today and silently wrong for
// any reformatting of it — a newline between the key and the value, or the key
// appearing inside another string, and it yields the wrong answer or none,
// with no way to tell which.
func cmdExpected(in io.Reader, out io.Writer) int {
	b, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read gate body: %v\n", err)
		return 1
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		fmt.Fprintf(os.Stderr, "gate body is not JSON: %v\n", err)
		return 1
	}
	v, ok := body["expected_version"].(string)
	if !ok || v == "" {
		// Exit 3, not 1: the caller must be able to distinguish "the gate did
		// not tell me" from "the gate is broken". Treating them alike is how
		// an unreadable gate came to kill the whole step.
		fmt.Fprintln(os.Stderr, "no expected_version in the gate body")
		return 3
	}
	fmt.Fprintln(out, v)
	return 0
}

// cmdMatch reports whether any image on stdin carries exactly the wanted tag.
func cmdMatch(in io.Reader, out io.Writer, want string) int {
	if want == "" {
		fmt.Fprintln(os.Stderr, "FAIL: -expected is empty, so this would accept any build")
		return 64
	}

	var refs []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			refs = append(refs, line)
		}
	}

	// A probe that inspected nothing is not a probe that found nothing. Falling
	// through to "no match" here would blame the version for what is really an
	// unreadable cluster.
	if len(refs) == 0 {
		fmt.Fprintln(os.Stderr, "FAIL: no container images were readable, so this run cannot")
		fmt.Fprintln(os.Stderr, "      tell which build it is about to test. That is a cluster")
		fmt.Fprintln(os.Stderr, "      or RBAC problem, not a version mismatch.")
		return 2
	}

	for _, ref := range refs {
		if imageTag(ref) == want {
			fmt.Fprintf(out, "%s\n", ref)
			return 0
		}
	}

	fmt.Fprintf(os.Stderr, "FAIL: no running image carries version %q. Examined %d image(s):\n", want, len(refs))
	for _, ref := range refs {
		fmt.Fprintf(os.Stderr, "        %s  (tag %q)\n", ref, imageTag(ref))
	}
	fmt.Fprintln(os.Stderr, "      The preview is serving a different build from the one the deploy")
	fmt.Fprintln(os.Stderr, "      intended, so every check in this run would describe that build.")
	return 1
}

// imageTag returns the tag of a reference, or "" if it carries none.
//
// The digest is stripped FIRST. jx pins images as name:tag@sha256:digest, so
// taking everything after the last colon yields the digest hex — the bug that
// failed every digest-pinned preview while it served exactly the right build.
//
// A registry port is why the last colon before the digest is not enough on its
// own either: registry:5000/app has a colon and no tag.
func imageTag(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon < 0 || colon < slash {
		// No colon at all, or the only colon is a registry port in the host
		// part (registry:5000/app), which is not a tag.
		return ""
	}
	return ref[colon+1:]
}
