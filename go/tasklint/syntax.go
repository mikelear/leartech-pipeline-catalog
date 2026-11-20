package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runSyntax asks `bash -n` about every step script and reports the first line of bash's
// own error. It runs nothing, needs no cluster, and takes about a second for the whole
// catalog.
//
// The failures this catches were real twice: three tasks had an `if … then` opened and
// never closed, and four more kept a `)` after a subshell was removed. Both make the step
// die before executing a line, so the visible symptom is "no coverage comment" or a bare
// failed step — never "the script is broken".
//
// proven-by: TestSyntaxReportsAnUnclosedIf
func runSyntax(steps []step, loadErrs []error) int {
	fmt.Println("=== every task step script parses ===")
	reportLoadErrors(loadErrs)
	fail := len(loadErrs)

	for _, s := range steps {
		f, err := os.CreateTemp("", "tasklint-*.sh")
		if err != nil {
			note("could not create a temp file for %s: %v", s.where(), err)
			fail++
			continue
		}
		path := f.Name()
		_, werr := f.WriteString(s.Script)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			note("could not write %s: %v%v", s.where(), werr, cerr)
			fail++
			_ = os.Remove(path)
			continue
		}

		out, err := exec.Command("bash", "-n", path).CombinedOutput()
		_ = os.Remove(path)
		if err == nil {
			continue
		}
		fail++
		first := "(no message)"
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); len(lines) > 0 && lines[0] != "" {
			first = lines[0]
		}
		note("%s does not parse: %s", s.where(), first)
	}

	if fail == 0 {
		fmt.Printf("  ok   %d step script(s) parse\n", countSteps(steps))
	}
	return boolToCode(fail == 0)
}

func boolToCode(ok bool) int {
	if ok {
		return 0
	}
	return 1
}
