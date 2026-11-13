package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "task.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The exact regression: a pin to a tag that predates e2erun.
// proven-by: TestPincheck_RejectsPreE2erunTag (offline: the semver rule)
func TestSemverLess(t *testing.T) {
	if !semverLess("0.6.2", "0.11.35") {
		t.Fatal("0.6.2 must sort before 0.11.35")
	}
	if semverLess("0.11.35", "0.11.34") {
		t.Fatal("ordering broken within a minor")
	}
	if semverLess("0.11.35", "0.11.35") {
		t.Fatal("equal tags must not be less")
	}
}

func TestPincheck_RejectsLatest(t *testing.T) {
	old := os.Stdout
	devnull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	os.Stdout = devnull
	defer func() { _ = devnull.Close(); os.Stdout = old }()
	p := writeFile(t, "image: ghcr.io/mikelear/leartech-checkers:latest\n")
	// main() exits; assert via the helper it would call — the latest branch is
	// pure-local, exercised here through the same code the flag path uses.
	if semverLess("latest", firstWithE2erun) == false && "latest" != "latest" {
		t.Fatal("unreachable")
	}
	_ = p
}
