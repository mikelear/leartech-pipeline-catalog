package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The declaration is the contract, so its defaults ARE the behaviour change: a
// repo that says nothing gets today's guess, and a repo that says the wrong thing
// is told. These cases are written as the estate's real declarations, so this
// file doubles as the documentation §5 of docs/end2end-preview.md points at.

func writeContract(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "end2end"), 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, ContractFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A complete declaration parses with every value taken from the file.
func TestLoadContractComplete(t *testing.T) {
	dir := writeContract(t, `preview:
  healthPath: /health
  service: leartech-sp-web
  port: 8080
  gate: required
  versionAnchor: false
  probes:
    - url: http://hydra.${NAMESPACE}.svc:4445/.well-known/openid-configuration
      want: 200
      note: hydra's discovery document
  seeds:
    - end2end/00-seed-test-data.sh
ui:
  probe: service
`)
	c, findings := LoadContract(dir)
	if c == nil {
		t.Fatalf("contract did not parse: %v", findings)
	}
	if len(findings) != 0 {
		t.Errorf("a complete declaration produced findings: %v", findings)
	}
	if c.HealthPath != "/health" || c.HealthFrom != "declared" {
		t.Errorf("healthPath = %q (%s), want /health (declared)", c.HealthPath, c.HealthFrom)
	}
	if c.Service != "leartech-sp-web" || c.Port != 8080 {
		t.Errorf("service/port = %q/%d", c.Service, c.Port)
	}
	if c.Gate != GateRequired {
		t.Errorf("gate = %q, want required", c.Gate)
	}
	if c.VersionAnchor {
		t.Error("versionAnchor: false was not honoured — the anchor would compare against a build the repo said not to check")
	}
	if len(c.Probes) != 1 || c.Probes[0].Want != 200 {
		t.Errorf("probes = %+v", c.Probes)
	}
	if c.UIProbe != "service" {
		t.Errorf("ui.probe = %q, want service", c.UIProbe)
	}
}

// next-generation-lending-website#9: the repo set END2END_HEALTH_PATH=/health,
// the bash lane probed /health/live, and the PR was failed for a preview that was
// serving. An undeclared healthPath is a finding, not a silent default.
func TestLoadContractMissingHealthPathIsAFinding(t *testing.T) {
	dir := writeContract(t, "preview:\n  service: thing\n")
	c, findings := LoadContract(dir)
	if c == nil {
		t.Fatal("contract did not parse")
	}
	if len(findings) == 0 {
		t.Fatal("an undeclared healthPath produced no finding; that is the silent default that failed next-generation-lending-website#9")
	}
	if !strings.Contains(findings[0], "healthPath") {
		t.Errorf("finding does not name healthPath: %q", findings[0])
	}
	if c.HealthFrom != "default" {
		t.Errorf("HealthFrom = %q, want default (so the explain output shows a guess)", c.HealthFrom)
	}
}

// A repo whose chart owns the probe path says so explicitly instead of guessing.
func TestLoadContractHealthPathFromChart(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPathSource: chart\n")
	c, findings := LoadContract(dir)
	if c == nil {
		t.Fatal("contract did not parse")
	}
	if len(findings) != 0 {
		t.Errorf("healthPathSource: chart produced findings: %v", findings)
	}
	if c.HealthFrom != "chart" || c.HealthPath != "" {
		t.Errorf("got %q (%s), want empty (chart)", c.HealthPath, c.HealthFrom)
	}
}

func TestLoadContractMissingFile(t *testing.T) {
	dir := writeContract(t, "")
	c, findings := LoadContract(dir)
	if c != nil {
		t.Error("a missing contract returned a contract")
	}
	if len(findings) == 0 || !strings.Contains(findings[0], "preview.yaml") {
		t.Errorf("findings = %v, want one naming the file", findings)
	}
}

func TestLoadContractUnparseable(t *testing.T) {
	dir := writeContract(t, "preview: [oops\n")
	c, findings := LoadContract(dir)
	if c != nil {
		t.Error("unparseable contract returned a contract")
	}
	if len(findings) == 0 || !strings.Contains(findings[0], "does not parse") {
		t.Errorf("findings = %v", findings)
	}
}

// A gate value that is not one of the three is a typo, and typing it must not
// quietly cost the detect window it was meant to remove.
func TestLoadContractBadGateMode(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  gate: yes-please\n")
	c, findings := LoadContract(dir)
	if c == nil {
		t.Fatal("did not parse")
	}
	if c.Gate != GateAuto {
		t.Errorf("gate = %q, want auto", c.Gate)
	}
	if len(findings) != 1 || !strings.Contains(findings[0], "auto|required|absent") {
		t.Errorf("findings = %v, want one listing the allowed values", findings)
	}
}

func TestLoadContractHealthPathMustBeRooted(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: health\n")
	_, findings := LoadContract(dir)
	found := false
	for _, f := range findings {
		if strings.Contains(f, "does not start with /") {
			found = true
		}
	}
	if !found {
		t.Errorf("an unrooted healthPath produced no finding: %v", findings)
	}
}

func TestLoadContractProbeDefaults(t *testing.T) {
	dir := writeContract(t, `preview:
  healthPath: /health
  probes:
    - url: http://x.${NAMESPACE}.svc/health
`)
	c, findings := LoadContract(dir)
	if c == nil {
		t.Fatal("did not parse")
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v", findings)
	}
	if len(c.Probes) != 1 || c.Probes[0].Want != 200 {
		t.Errorf("probes = %+v, want want=200 by default", c.Probes)
	}
}

func TestLoadContractProbeWithoutURL(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health\n  probes:\n    - want: 200\n")
	_, findings := LoadContract(dir)
	found := false
	for _, f := range findings {
		if strings.Contains(f, "has no url") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings = %v, want one about the missing url", findings)
	}
}

// The UI lane's seed sweep runs whatever it finds. Naming a check as a seed is how
// the auth-service functional suite came to run twice with failures swallowed.
func TestSeedNameLooksLikeACheck(t *testing.T) {
	cases := []struct {
		name  string
		check bool
	}{
		{"end2end/00-register-oauth-client.sh", false},
		{"end2end/00-seed-test-data.sh", false},
		{"end2end/00-setup-users.sh", false},
		{"end2end/00-reset-fixtures.sh", false},
		{"end2end/00-bootstrap.sh", false},
		{"end2end/01-smoke.sh", true},
		{"end2end/02-oauth-flow.sh", true},
		{"end2end/05-auth-refusals.sh", true},
		{"end2end/03-fleet-test.sh", true},
	}
	for _, c := range cases {
		if got := SeedNameLooksLikeACheck(c.name); got != c.check {
			t.Errorf("SeedNameLooksLikeACheck(%q) = %v, want %v", c.name, got, c.check)
		}
	}
}

func TestContractStringShowsDefaults(t *testing.T) {
	dir := writeContract(t, "preview:\n  healthPath: /health/live\n")
	c, _ := LoadContract(dir)
	out := c.String()
	for _, want := range []string{"/health/live", "[declared]", "gate:          auto", "versionAnchor: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output missing %q:\n%s", want, out)
		}
	}
}
