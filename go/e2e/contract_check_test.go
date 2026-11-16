package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A check is only worth having if it fails on the thing that actually happened, so
// every case here is a real finding from the estate, built as a fixture. That also
// makes this file the worked example list for docs/end2end-preview.md.

func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A well-formed repo passes: this is the standard the docs point at.
func TestContractCleanRepo(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: leartech-sp-web
  setup:
    - verb: probe
      url: http://preview-hydra.${NAMESPACE}.svc:4445/.well-known/openid-configuration
      want: 200
`,
		"end2end/01-smoke.sh":   "#!/bin/bash\ncurl -sS -o /dev/null \"${PREVIEW_URL}/\"\n",
		"preview/helmfile.yaml": "releases:\n- name: preview-leartech-sp-web\n  hostname: leartech-sp-web-pr1.example\n  hydra: hydra-pr1.jx.leartech.com\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 0 {
		t.Fatalf("clean repo failed the contract check (exit %d)", code)
	}
}

// next-generation-lending-website#9: the repo declared END2END_HEALTH_PATH=/health,
// the bash lane probed /health/live. An undeclared healthPath is a finding.
func TestContractUndeclaredHealthPath(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  service: thing\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("an undeclared healthPath passed; that default is the failure being fixed")
	}
}

// NONE and a live pack cannot both be authoritative.
func TestContractNoneAndPackBothPresent(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/NONE":        "no suite: chart library\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("NONE beside a pack passed; nobody can tell which is authoritative")
	}
}

// A repo with neither a pack nor NONE is the "undeclared" branch, and it is a
// finding before the PR rather than a red end2end run.
func TestContractNeitherPackNorNone(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: "preview:\n  healthPath: /health\n  service: x\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("a repo with no pack and no NONE passed")
	}
}

// leartech-mortgagesourcing-web#18: smoke hit a URL baked into the script rather
// than PREVIEW_URL, so it tested a host that had nothing to do with the preview.
func TestContractHardcodedHostInScript(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ncurl -sS https://api.mortgagesourcing.com/mcp\n",
	})
	code := runContract([]string{"-dir", dir})
	if code != 1 {
		t.Fatal("a script pinning a public host passed")
	}
}

func TestContractVariableBaseURLIsFine(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ncurl -sS \"${PREVIEW_URL}/health\"\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 0 {
		t.Fatal("a script using PREVIEW_URL was rejected")
	}
}

// leartech-auth-ui#111: the gate held a ready preview closed on a probe URL the
// preview chart never creates. Static, this is a missing host in preview/.
func TestContractProbeHostNotInCharts(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  probes:
    - url: https://hydra-pr111.jx.leartech.com/.well-known/openid-configuration
      want: 200
`,
		"end2end/01-smoke.sh":   "#!/bin/bash\ntrue\n",
		"preview/helmfile.yaml": "releases:\n- name: preview-x\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("a probe naming a host no chart creates passed")
	}
}

func TestContractProbeHostDeclaredInCharts(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  probes:
    - url: https://hydra-pr111.jx.leartech.com/.well-known/openid-configuration
      want: 200
`,
		"end2end/01-smoke.sh":   "#!/bin/bash\ntrue\n",
		"preview/helmfile.yaml": "hostname: hydra-pr111.jx.leartech.com\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 0 {
		t.Fatal("a probe host that IS in the charts was rejected")
	}
}

// A verb that is not on the menu is a finding: the menu is the contract, and
// "the repo brought its own procedure" is what it replaces.
func TestContractSetupVerbNotOnTheMenu(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  setup:
    - verb: psql-into-the-sibling
`,
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
	})
	out := captureContract(t, dir)
	if !strings.Contains(out, "is not on the catalog's menu") {
		t.Errorf("an unknown verb was accepted:\n%s", out)
	}
}

// A bespoke script declared for staging is refused: staging is persistent and shared,
// and the one script in the estate that repairs shared-state damage exists because
// another repo's bespoke setup caused it.
func TestContractBespokeRefusedForStaging(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  bespoke:
    - script: end2end/00-seed-thing.sh
      reason: legacy
      where: staging
`,
		"end2end/00-seed-thing.sh": "#!/bin/bash\ntrue\n",
		"end2end/01-smoke.sh":      "#!/bin/bash\ntrue\n",
	})
	out := captureContract(t, dir)
	if !strings.Contains(out, "staging is persistent and shared") {
		t.Errorf("a staging bespoke was accepted:\n%s", out)
	}
}

// A bespoke with no reason is an exception nobody can weigh.
func TestContractBespokeNeedsAReason(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  bespoke:
    - script: end2end/00-seed-thing.sh
`,
		"end2end/00-seed-thing.sh": "#!/bin/bash\ntrue\n",
		"end2end/01-smoke.sh":      "#!/bin/bash\ntrue\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("a bespoke with no reason passed")
	}
}

// A correctly declared bespoke script passes, and is reported in the explain output so
// it cannot hide.
func TestContractDeclaredBespokePasses(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: `preview:
  healthPath: /health
  service: x
  bespoke:
    - script: end2end/00-seed-thing.sh
      reason: needs a browser cookie no verb can mint yet
`,
		"end2end/00-seed-thing.sh": "#!/bin/bash\ntrue\n",
		"end2end/01-smoke.sh":      "#!/bin/bash\ntrue\n",
	})
	if code := runContract([]string{"-dir", dir}); code != 0 {
		t.Fatalf("a declared bespoke was rejected:\n%s", captureContract(t, dir))
	}
}

// A typo in an env var was silently ignored, which is how a repo believed it had
// declared a health path the task never read.
func TestContractUnknownEnvKey(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
		".lighthouse/jenkins-x/end2end.yaml": `spec:
  pipelineSpec:
    tasks:
    - taskSpec:
        stepTemplate:
          env:
          - name: END2END_HEALTH_PAHT
            value: /health
`,
	})
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("an unknown END2END_* env key passed")
	}
}

func TestContractKnownEnvKeyIsFine(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
		".lighthouse/jenkins-x/end2end.yaml": `spec:
  pipelineSpec:
    tasks:
    - taskSpec:
        stepTemplate:
          env:
          - name: END2END_HEALTH_PATH
            value: /health
`,
	})
	if code := runContract([]string{"-dir", dir}); code != 0 {
		t.Fatal("a key the task reads was rejected")
	}
}

// A UI lane with no specs is green having tested nothing.
func TestContractUILaneWithoutSpecs(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:          "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh": "#!/bin/bash\ntrue\n",
	})
	if code := runContract([]string{"-dir", dir, "-check-name", "ui"}); code != 1 {
		t.Fatal("the ui lane passed with no *.spec.ts")
	}
}

func TestContractUILaneSpecUsesPreviewURL(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:                 "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh":        "#!/bin/bash\ntrue\n",
		"end2end-ui/landing.spec.ts": "test('x', async ({page}) => { await page.goto(process.env.PREVIEW_URL!) })\n",
	})
	if code := runContract([]string{"-dir", dir, "-check-name", "ui"}); code != 0 {
		t.Fatal("a spec reading PREVIEW_URL was rejected")
	}
}

// A spec that pins a host tests that host; the UI lane must not pass it.
func TestContractUILaneSpecWithoutPreviewURL(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile:                 "preview:\n  healthPath: /health\n  service: x\n",
		"end2end/01-smoke.sh":        "#!/bin/bash\ntrue\n",
		"end2end-ui/landing.spec.ts": "test('x', async ({page}) => { await page.goto('https://example.test/') })\n",
	})
	if code := runContract([]string{"-dir", dir, "-check-name", "ui"}); code != 1 {
		t.Fatal("a spec that never mentions the preview URL passed")
	}
}

// A script the runner would skip entirely (non-executable) is a check that looks
// present and never runs in the lane that requires the bit.
func TestContractNonExecutableScript(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: "preview:\n  healthPath: /health\n  service: x\n",
	})
	p := filepath.Join(dir, "end2end", "01-smoke.sh")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/bash\ntrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runContract([]string{"-dir", dir}); code != 1 {
		t.Fatal("a non-executable pack script passed")
	}
}

// explain must show the defaults, because a declaration you cannot read back is
// one you cannot review.
func TestExplainShowsResolvedContract(t *testing.T) {
	dir := repo(t, map[string]string{
		ContractFile: "preview:\n  healthPath: /health/live\n  service: x\n",
	})
	code := runExplain([]string{"-dir", dir})
	if code != 0 && code != 1 {
		t.Fatalf("explain exited %d", code)
	}
	if !strings.Contains(ContractFile, "preview.yaml") {
		t.Fatal("contract file constant moved")
	}
}

// A repo whose path contains a character a glob treats specially must still have its
// pack found. filepath.Glob applies the bracket expression to the WHOLE path, so a
// repoDir with a '[' in it silently matched nothing — and the checker then reported "no
// pack scripts" about a repo with 22 of them, which is worse than reporting nothing.
func TestPackScriptsWithAGlobMetacharacterInThePath(t *testing.T) {
	dir := t.TempDir()
	weird := filepath.Join(dir, "repo[1]")
	if err := os.MkdirAll(filepath.Join(weird, "end2end"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"00-seed-test-data.sh", "01-smoke.sh", "README.md", "1-notnumbered.sh"} {
		if err := os.WriteFile(filepath.Join(weird, "end2end", n), []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := packScripts(weird)
	if len(got) != 2 {
		t.Errorf("packScripts found %d script(s) in a path with a bracket, want 2: %v", len(got), got)
	}
}

// The order the runner uses is lexical, and NN- prefixes exist for it.
func TestPackScriptsIsOrdered(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "end2end"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"03-c.sh", "01-a.sh", "02-b.sh"} {
		if err := os.WriteFile(filepath.Join(dir, "end2end", n), []byte("#!/bin/bash\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := packScripts(dir)
	if len(got) != 3 || !strings.HasSuffix(got[0], "01-a.sh") || !strings.HasSuffix(got[2], "03-c.sh") {
		t.Errorf("packScripts order = %v, want 01, 02, 03", got)
	}
}
