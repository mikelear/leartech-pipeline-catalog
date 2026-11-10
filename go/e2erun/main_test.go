package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePack(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// REAL pack shapes: the same scripts repos ship (pass / fail-with-output / skip-77).
func TestRunPack_PassFailSkipMatrix(t *testing.T) {
	dir := writePack(t, map[string]string{
		"01-smoke.sh":   "#!/bin/bash\necho ok\n",
		"02-scope.sh":   "#!/bin/bash\necho 'token admitted on /admin'\nexit 1\n",
		"03-staging.sh": "#!/bin/bash\necho 'preview-only check'\nexit 77\n",
	})
	res := RunPack(dir)
	if res.Success {
		t.Fatal("a failing check must fail the pack")
	}
	if len(res.Tests) != 3 {
		t.Fatalf("tests=%d", len(res.Tests))
	}
	if res.Tests[0].Status != "pass" || res.Tests[1].Status != "fail" || res.Tests[2].Status != "skip" {
		t.Fatalf("statuses wrong: %+v", res.Tests)
	}
	if res.Tests[1].Message == "(no output)" {
		t.Fatal("a failure must carry the log tail, not the empty placeholder")
	}
	if res.Summary != "1/2 checks passed" {
		t.Fatalf("summary=%q", res.Summary)
	}
}

func TestRunPack_StripOrderingPrefix(t *testing.T) {
	dir := writePack(t, map[string]string{
		"04-renumbered.sh": "#!/bin/bash\ntrue\n",
	})
	res := RunPack(dir)
	if res.Tests[0].Name != "renumbered" {
		t.Fatalf("name=%q — renumbering must not change identity", res.Tests[0].Name)
	}
}

func TestRunPack_AllSkippedIsPassWithExplicitSummary(t *testing.T) {
	dir := writePack(t, map[string]string{
		"01-preview-only.sh": "#!/bin/bash\necho 'no preview on staging'\nexit 77\n",
	})
	res := RunPack(dir)
	if !res.Success {
		t.Fatal("all-skipped is a pass, or a staging pack of preview checks fails promotions")
	}
	if res.Summary != "0/0 checks passed, 1 skipped — nothing applicable ran in this environment" {
		t.Fatalf("summary must say nothing ran, got %q", res.Summary)
	}
}

func TestRunPack_FailureCarriesTheLogTail(t *testing.T) {
	dir := writePack(t, map[string]string{
		"01-deep.sh": "#!/bin/bash\necho line1\necho line2\necho 'PER-ROUTE SCOPE ENFORCEMENT BROKEN: admin token admitted'\nexit 1\n",
	})
	res := RunPack(dir)
	if !contains(res.Tests[0].Message, "PER-ROUTE SCOPE ENFORCEMENT BROKEN") {
		t.Fatalf("message=%q — the diagnostic line must survive", res.Tests[0].Message)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestResultsJSONShape(t *testing.T) {
	dir := writePack(t, map[string]string{"01-a.sh": "#!/bin/bash\ntrue\n"})
	res := RunPack(dir)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"success", "summary", "tests"} {
		if _, ok := back[k]; !ok {
			t.Fatalf("results.json missing %q", k)
		}
	}
}
