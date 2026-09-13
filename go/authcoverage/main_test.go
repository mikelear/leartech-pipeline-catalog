package main

import (
	"os"
	"path/filepath"
	"testing"
)

// mkRepo builds a throwaway repo tree. Every test below differs from another by
// exactly one property, so a pass attributes to that property.
func mkRepo(t *testing.T, profile string, testNames []string, e2e map[string]string, umbrella bool) string {
	t.Helper()
	dir := t.TempDir()
	if profile != "" {
		must(t, os.WriteFile(filepath.Join(dir, ".authprofile"), []byte("type: "+profile+"\n"), 0o600))
	}
	body := "package x\n"
	for _, n := range testNames {
		body += "func " + n + "(t *testing.T) {}\n"
	}
	must(t, os.WriteFile(filepath.Join(dir, "x_test.go"), []byte(body), 0o600))
	if len(e2e) > 0 {
		must(t, os.MkdirAll(filepath.Join(dir, "end2end"), 0o755))
		for name, content := range e2e {
			must(t, os.WriteFile(filepath.Join(dir, "end2end", name), []byte(content), 0o600))
		}
	}
	must(t, os.MkdirAll(filepath.Join(dir, "preview"), 0o755))
	hf := "releases:\n- chart: ../charts/x\n"
	if umbrella {
		hf = "releases:\n- chart: leartech/leartech-preview-infrastructure\n"
	}
	must(t, os.WriteFile(filepath.Join(dir, "preview", "helmfile.yaml.gotmpl"), []byte(hf), 0o600))
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// THE CASE THIS TOOL EXISTS FOR: unit proof, no deployed proof.
func TestUnitTestsWithoutDeployedProof_IsFlagged(t *testing.T) {
	d := mkRepo(t, "inbound-resource-server",
		[]string{"TestRFC8707_AudienceForAnotherService_IsRefused"},
		map[string]string{"01-smoke.sh": "curl -sS $PREVIEW_URL/health\n"}, false)

	r := scan(d)
	if r.Err != "" {
		t.Fatalf("scan failed: %s", r.Err)
	}
	if r.unitTotal() == 0 {
		t.Fatal("no auth unit tests counted — the name matcher is broken, not the repo")
	}
	if got := unproven([]repo{r}); len(got) != 1 {
		t.Fatalf("a repo with auth unit tests and an end2end that touches no token should be "+
			"flagged; got %d gaps. This is the state artifact-api was in: its script said "+
			"outright it could not exercise the authenticated paths.", len(got))
	}
}

// The control. Same repo, one difference: the end2end obtains a token.
func TestDeployedProof_IsNotFlagged(t *testing.T) {
	d := mkRepo(t, "inbound-resource-server",
		[]string{"TestRFC8707_AudienceForAnotherService_IsRefused"},
		map[string]string{"02-auth.sh": "curl -X POST $HYDRA/oauth2/token -d grant_type=client_credentials\n"}, true)

	r := scan(d)
	if len(r.E2EAuth) != 1 {
		t.Fatalf("expected the token-obtaining script to count as deployed proof, got %v", r.E2EAuth)
	}
	if got := unproven([]repo{r}); len(got) != 0 {
		t.Fatalf("a repo whose end2end mints a token must not be flagged; got %d", len(got))
	}
}

// Listing scripts is not evidence. artifact-api had three end2end scripts and
// none of them touched a token, which is exactly why the column reports the
// script NAMES that do rather than a count of files.
func TestScriptsThatNeverTouchATokenAreNotProof(t *testing.T) {
	d := mkRepo(t, "inbound-resource-server", []string{"TestRFC6749_3_3_NoScopes_IsRefused"},
		map[string]string{
			"01-smoke.sh": "curl $PREVIEW_URL/health\n",
			"02-api.sh":   "curl $PREVIEW_URL/openapi.json\n",
			"03-other.sh": "echo nothing to see\n",
		}, true)

	r := scan(d)
	if r.E2EScripts != 3 {
		t.Fatalf("expected 3 scripts counted, got %d", r.E2EScripts)
	}
	if len(r.E2EAuth) != 0 {
		t.Fatalf("scripts touching no token must not count as auth proof, got %v", r.E2EAuth)
	}
}

// A repo declaring `none` has decided, and a decision is not a gap.
func TestDeclaredNone_IsNotFlagged(t *testing.T) {
	d := mkRepo(t, "none", []string{"TestRFC8707_Something"}, nil, false)
	if got := unproven([]repo{scan(d)}); len(got) != 0 {
		t.Fatalf("a repo declaring type: none must not be flagged; got %d", len(got))
	}
}

// THE REGRESSION. WalkDir does not follow symlinks, so a symlinked repo
// produced a row of zeros — reporting "no coverage" for a repo never read.
// Reporting absence you did not verify is the defect this tool exists to find.
func TestSymlinkedRepoIsStillScanned(t *testing.T) {
	real := mkRepo(t, "dual-role", []string{"TestRFC8707_AudienceForAnotherService_IsRefused"},
		map[string]string{"01-auth.sh": "oauth2/token\n"}, true)

	link := filepath.Join(t.TempDir(), "leartech-linked")
	must(t, os.Symlink(real, link))

	r := scan(link)
	if r.Err != "" {
		t.Fatalf("scan of a symlinked repo failed: %s", r.Err)
	}
	if r.unitTotal() == 0 || len(r.E2EAuth) == 0 {
		t.Fatalf("symlinked repo scanned as empty (unit=%d, e2e-auth=%v). WalkDir does not "+
			"follow symlinks, so this silently reported zero coverage for a repo it "+
			"never read.", r.unitTotal(), r.E2EAuth)
	}
}

// A path with no Go tests means the walk found nothing, not that the repo has
// no tests. Reporting zeros there is a confident wrong answer.
func TestPathWithNoGoTests_ReportsAnError_NotZeroCoverage(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("no tests here"), 0o600))

	r := scan(dir)
	if r.Err == "" {
		t.Fatal("a directory with no _test.go must report an error rather than a row of " +
			"zeros — otherwise the tool answers 'no auth coverage' for a path it could not read")
	}
	if got := unproven([]repo{r}); len(got) != 0 {
		t.Fatalf("an errored repo must not be reported as a coverage gap; got %d", len(got))
	}
}

// Anchor: the category matcher must actually match the estate's real names. If
// these stop matching, every count silently drops to zero.
func TestCategoryPatternsMatchRealEstateTestNames(t *testing.T) {
	cases := map[string]string{
		"TestRFC8707_AudienceForAnotherService_IsRefused": "audience",
		"TestRFC6749_3_3_ReadScopeDoesNotGrantWrite":      "scope",
		"TestRFC7515_SignatureFromAForeignKey_IsRefused":  "issuer/signature",
		"TestDCR_DisallowedScope_Returns400":              "DCR",
		"TestRFC9728_ProtectedResourceMetadata_IsServed":  "DCR",
	}
	for name, want := range cases {
		hit := ""
		for _, c := range categories {
			if c.re.MatchString(name) {
				hit = c.name
				break
			}
		}
		if hit == "" {
			t.Errorf("%s matched no category — the estate uses this naming, so the matrix would undercount", name)
		}
		_ = want
	}
}
