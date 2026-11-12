// e2erun executes a repo's end2end pack scripts and assembles results.json.
//
// Extracted from tasks/end2end/pullrequest.yaml's inline runner (the ~200-line
// loop with skip-exit semantics, name stripping, message tails and the
// empty/skip/green verdict matrix) so that logic is Go-tested instead of
// shell-trusted. The task keeps what shell is for: waiting on previews and
// kubectl; this keeps what tests are for: the verdict rules.
//
// Exit codes: 0 the pack passed (or nothing applicable ran — reported as pass
// with an explicit summary); 1 a check failed or the pack crashed. 77 from a
// script is SKIP, never failure.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SkipExit is the code a script uses to say "does not apply here".
const SkipExit = 77

// Test is one row of results.json.
type Test struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
	Message    string `json:"message"`
}

// Results is the schema the task contract documents.
type Results struct {
	Success  bool            `json:"success"`
	Summary  string          `json:"summary"`
	Tests    []Test          `json:"tests"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// metadataFor assembles the metadata object from the environment the runner
// historically built in jq: preview_url + version, plus any end2end/doc/*.md
// fragments concatenated into doc.
//
// proven-by: TestMetadataFor_MergesDocFragmentsAndEnv
func metadataFor(dir string) json.RawMessage {
	meta := map[string]any{}
	if v := os.Getenv("PREVIEW_URL"); v != "" {
		meta["preview_url"] = v
	}
	if v := os.Getenv("VERSION"); v != "" {
		meta["version"] = v
	}
	docs, _ := filepath.Glob(filepath.Join(dir, "doc", "*.md"))
	if len(docs) > 0 {
		sort.Strings(docs)
		parts := make([]string, 0, len(docs))
		for _, d := range docs {
			b, err := os.ReadFile(d)
			if err == nil {
				parts = append(parts, string(b))
			}
		}
		if len(parts) > 0 {
			meta["doc"] = strings.Join(parts, "\n")
		}
		fmt.Printf("[end2end] collected %d doc fragment(s) for the PR comment\n", len(docs))
	}
	if len(meta) == 0 {
		return nil
	}
	b, _ := json.Marshal(meta)
	return b
}

// RunPack executes every NN-*.sh under dir, in order, and returns the results.
//
// proven-by: TestRunPack_PassFailSkipMatrix
// proven-by: TestRunPack_StripOrderingPrefix
// proven-by: TestRunPack_AllSkippedIsPassWithExplicitSummary
// proven-by: TestRunPack_FailureCarriesTheLogTail
func RunPack(dir string) Results {
	scripts, _ := filepath.Glob(filepath.Join(dir, "[0-9][0-9]-*.sh"))
	sort.Strings(scripts)
	var (
		passed, failed, skipped int
		tests                   []Test
	)
	for _, script := range scripts {
		name := strings.TrimSuffix(filepath.Base(script), ".sh")
		name = strings.TrimLeft(name, "0123456789")
		name = strings.TrimPrefix(name, "-")
		log := filepath.Join(dir, name+".log")
		t0 := time.Now()
		cmd := exec.Command("bash", script)
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		_ = os.WriteFile(log, out, 0o644)
		dur := time.Since(t0).Milliseconds()

		status, message := "pass", "OK"
		switch code := cmd.ProcessState.ExitCode(); code {
		case 0:
			passed++
		case SkipExit:
			status = "skip"
			message = tailMessage(out, "(skipped, no reason given)")
			skipped++
			fmt.Printf("[end2end] SKIP %s: %s\n", name, message)
		default:
			status = "fail"
			message = tailMessage(out, "(no output)")
			fmt.Printf("[end2end] ---- %s failed, full log ----\n%s\n[end2end] ---- end %s ----\n", name, string(out), name)
			failed++
		}
		tests = append(tests, Test{Name: name, Status: status, DurationMS: dur, Message: message})
	}

	total := passed + failed
	summary := "no 0X-*.sh scripts found"
	success := false
	switch {
	case len(scripts) == 0:
		// The task contract: an empty pack directory is a pass with a warning
		// row; absence of the DIRECTORY is the repo's declared end2end/NONE.
		success = true
		summary = "no 0X-*.sh scripts found"
	case failed == 0 && passed == 0 && skipped > 0:
		success = true
		summary = fmt.Sprintf("%d/%d checks passed, %d skipped — nothing applicable ran in this environment", passed, total, skipped)
	case failed > 0:
		summary = fmt.Sprintf("%d/%d checks passed", passed, total)
	case failed == 0:
		success = true
		if skipped > 0 {
			summary = fmt.Sprintf("%d/%d checks passed, %d skipped", passed, total, skipped)
		} else {
			summary = fmt.Sprintf("%d/%d checks passed", passed, total)
		}
	}
	return Results{Success: success, Summary: summary, Tests: tests}
}

func tailMessage(out []byte, empty string) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		return empty
	}
	tail := lines
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	msg := strings.Join(tail, " ")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

func main() {
	dir := "end2end"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	res := RunPack(dir)
	res.Metadata = metadataFor(dir)
	b, _ := json.MarshalIndent(res, "", "  ")
	out := filepath.Join(dir, "results.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "e2erun:", err)
		os.Exit(1)
	}
	fmt.Println(res.Summary)
	if !res.Success {
		os.Exit(1)
	}
	_ = strconv.Itoa // keep import parity with future use
}
