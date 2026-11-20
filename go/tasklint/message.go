package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// The message-layer ratchet: how much prose a task composes in shell.
//
// Two measures, both about a shell step deciding what to say:
//
//	md    a line whose whole content is prose, a table row, an emoji verdict or the
//	      "_Posted by_" footer. That is the body of a comment.
//	echo  an echo/printf that interpolates something. That is how a value becomes a
//	      verdict in a sentence.
//
// A verdict composed in shell has no test, because shell has nowhere to put one. The
// ratchet makes that cost visible at the moment it grows and invisible when it shrinks —
// which is what keeps the number going one way.
var (
	markdownLine = regexp.MustCompile(`^"?(###|:x:|:white_check_mark:|:warning:|:fast_forward:|:information_source:|:large_blue_circle:|\| |\*\*)`)

	// `| sed -n 's/…/…/' | head -1` is a PIPELINE, not a table row. A table ROW has at
	// least two pipes — the leading one and the one that ends its first cell — and a
	// pipeline continuation has exactly one. The first version of this check kept a list
	// of shell binaries instead, which can only ever be incomplete: `| tar -xz -C …`
	// counted as prose until the list was fixed, and the list is why it took a false
	// positive on three files to notice.
	shellAfterPipe = regexp.MustCompile(`^\|\s*(sed|grep|awk|jq|head|tail|sort|cut|tr|wc|curl|cat|xargs|tee|uniq|paste|base64|tar|mv|cp|chmod|sh|bash|export|source)\b`)

	// NOT every interpolated printf is prose. `printf 'coverage=%s\n' "$TOTAL"` pipes a
	// VALUE; it says nothing to a reader, and counting it would push a step to keep its
	// formatting in shell to avoid the number going up. After stripping conversions and
	// separators, a bare key name is data: `coverage=%s` says a value to a file,
	// `Coverage: %s%%` says something to a person.
	dataOnly = regexp.MustCompile(`^[a-z_]+$`)

	echoOrPrintf     = regexp.MustCompile(`^(echo|printf)\b`)
	formatConversion = regexp.MustCompile(`%[-#0-9.]*[a-zA-Z]|\\n|\\t|\\\\`)

	// Go's regexp has no backreferences, so the two quote styles are two patterns rather
	// than one with a \1. Both capture the literal text inside the quotes.
	echoDoubleQuoted = regexp.MustCompile(`^(?:echo|printf)\s+"(.*?)"`)
	echoSingleQuoted = regexp.MustCompile(`^(?:echo|printf)\s+'(.*?)'`)
)

func isMarkdown(line string) bool {
	if shellAfterPipe.MatchString(line) {
		return false
	}
	if strings.HasPrefix(line, "|") && strings.Count(line, "|") < 2 {
		return false
	}
	return markdownLine.MatchString(line) || strings.Contains(line, `"_Posted by`)
}

// isProse reports whether an echo/printf's own text would be read by a person.
//
// proven-by: TestIsProseIgnoresAValueInterpolation
func isProse(line string) bool {
	var fmtText string
	switch {
	case echoDoubleQuoted.MatchString(line):
		fmtText = echoDoubleQuoted.FindStringSubmatch(line)[1]
	case echoSingleQuoted.MatchString(line):
		fmtText = echoSingleQuoted.FindStringSubmatch(line)[1]
	default:
		// printf with an unquoted or variable format: assume prose, since we cannot tell.
		return true
	}
	stripped := formatConversion.ReplaceAllString(fmtText, "")
	stripped = strings.Trim(stripped, ` '"=`)
	return !dataOnly.MatchString(stripped)
}

func isInterpolating(line string) bool {
	if !echoOrPrintf.MatchString(line) {
		return false
	}
	return strings.Contains(line, "$") || strings.Contains(line, `"`)
}

// layerCount is one task file's contribution to the two totals.
type layerCount struct {
	Markdown int
	Echo     int
}

func measure(steps []step) map[string]layerCount {
	out := map[string]layerCount{}
	for _, s := range steps {
		c := out[s.File]
		for _, raw := range strings.Split(s.Script, "\n") {
			line := strings.TrimSpace(raw)
			if isMarkdown(line) {
				c.Markdown++
			}
			if isInterpolating(line) && isProse(line) {
				c.Echo++
			}
		}
		out[s.File] = c
	}
	// A file with neither measure is not part of the population; keeping it would make
	// the baseline grow every time a task is added with no shell at all.
	for f, c := range out {
		if c.Markdown == 0 && c.Echo == 0 {
			delete(out, f)
		}
	}
	return out
}

// parseBaseline reads `<markdown> <echo> <path>` lines. A comment or a blank line is
// skipped; anything else that does not split into three fields is reported, because a
// baseline that cannot be read must not read as empty.
func parseBaseline(path string) (map[string]layerCount, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- the operator names the baseline
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]layerCount{}, nil
		}
		return nil, err
	}
	out := map[string]layerCount{}
	for i, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Fields(t)
		if len(fields) < 3 {
			return nil, fmt.Errorf("%s:%d: want `<markdown> <echo> <path>`, got %q", path, i+1, t)
		}
		var md, echos int
		if _, err := fmt.Sscanf(fields[0], "%d", &md); err != nil {
			return nil, fmt.Errorf("%s:%d: markdown count %q is not a number", path, i+1, fields[0])
		}
		if _, err := fmt.Sscanf(fields[1], "%d", &echos); err != nil {
			return nil, fmt.Errorf("%s:%d: echo count %q is not a number", path, i+1, fields[1])
		}
		out[strings.Join(fields[2:], " ")] = layerCount{Markdown: md, Echo: echos}
	}
	return out, nil
}

// runMessage applies the ratchet. Growth fails; a shrink is a note, because the baseline
// has to be lowered in the same PR that earns it and a note is what tells the author to.
//
// proven-by: TestMessageRatchetFailsOnGrowth
func runMessage(steps []step, loadErrs []error, baselinePath string) int {
	fmt.Println("=== verdict-and-message logic written in shell (ratchet) ===")
	reportLoadErrors(loadErrs)

	baseline, err := parseBaseline(baselinePath)
	if err != nil {
		note("%v", err)
		return 1
	}
	current := measure(steps)

	totalMD, totalEcho := 0, 0
	for _, c := range current {
		totalMD += c.Markdown
		totalEcho += c.Echo
	}
	fmt.Printf("  %d task(s) compose a verdict or a message in shell\n", len(current))
	fmt.Printf("  %d markdown line(s), %d interpolated echo/printf line(s)\n\n", totalMD, totalEcho)

	var grew []string
	for _, f := range sortedKeys(current) {
		c := current[f]
		b, ok := baseline[f]
		if !ok {
			grew = append(grew, fmt.Sprintf("%s: new: %d markdown, %d echo — not in the baseline", f, c.Markdown, c.Echo))
			continue
		}
		if c.Markdown > b.Markdown || c.Echo > b.Echo {
			grew = append(grew, fmt.Sprintf("%s: grew from %d/%d to %d/%d (markdown/echo)",
				f, b.Markdown, b.Echo, c.Markdown, c.Echo))
		}
	}
	if len(grew) > 0 {
		fmt.Println("  FAIL the message layer grew:")
		for _, g := range grew {
			fmt.Printf("       %s\n", g)
		}
		fmt.Println()
		fmt.Println("A verdict or a sentence composed in shell has no test, because shell has")
		fmt.Println("nowhere to put one. Move it into a Go reporter and lower its line in")
		fmt.Printf("%s in the SAME PR — that is what makes this a ratchet.\n", baselinePath)
		return 1
	}

	var shrank []string
	for _, f := range sortedKeys(baseline) {
		b := baseline[f]
		c := current[f]
		if c.Markdown < b.Markdown || c.Echo < b.Echo {
			shrank = append(shrank, fmt.Sprintf("%s: %d/%d -> %d/%d", f, b.Markdown, b.Echo, c.Markdown, c.Echo))
		}
	}
	if len(shrank) > 0 {
		fmt.Println("  Note: these moved the right way — update the baseline so it stays down:")
		for _, s := range shrank {
			fmt.Printf("       %s\n", s)
		}
		fmt.Println()
	}

	fmt.Println("  ok   no task added shell-composed verdicts or prose")
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
