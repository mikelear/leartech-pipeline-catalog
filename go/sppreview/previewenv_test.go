package main

import (
	"strings"
	"testing"
)

// The golden template's preview helmfile is a set of requiredEnv calls, so a missing variable is a
// failure BEFORE anything is rendered — and jx preview used to supply them.
//
// proven-by: TestPreviewEnv_CarriesEveryKeyTheHelmfileRequires
func TestPreviewEnv_CarriesEveryKeyTheHelmfileRequires(t *testing.T) {
	base := []string{"DOCKER_REGISTRY=reg", "DOCKER_REGISTRY_ORG=org", "VERSION=0.0.0-PR-2-1-SNAPSHOT"}

	env, err := previewEnv("jx-it-test-app-pr-2", "app", "2", base)
	if err != nil {
		t.Fatalf("previewEnv: %v", err)
	}

	got := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, k := range requiredPreviewEnv {
		if got[k] == "" {
			t.Errorf("the helmfile requires %s and the environment does not carry it", k)
		}
	}
	if got["PREVIEW_NAMESPACE"] != "jx-it-test-app-pr-2" {
		t.Errorf("PREVIEW_NAMESPACE = %q, want the namespace sppreview deploys into", got["PREVIEW_NAMESPACE"])
	}
}

// A control: a value the caller exported is not silently dropped, and the derived one wins over a
// stale copy so the namespace the releases land in is the one this binary waited on.
func TestPreviewEnv_DerivedValuesWinAndTheRestSurvive(t *testing.T) {
	base := []string{
		"PREVIEW_NAMESPACE=stale", "APP_NAME=app", "PULL_NUMBER=2",
		"DOCKER_REGISTRY=reg", "DOCKER_REGISTRY_ORG=org", "VERSION=1",
	}
	env, err := previewEnv("jx-it-test-app-pr-2", "app", "2", base)
	if err != nil {
		t.Fatalf("previewEnv: %v", err)
	}

	counts := map[string]int{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		counts[k]++
		if k == "PREVIEW_NAMESPACE" && v != "jx-it-test-app-pr-2" {
			t.Errorf("PREVIEW_NAMESPACE = %q, want the derived namespace", v)
		}
	}
	for k, n := range counts {
		if n > 1 {
			t.Errorf("%s appears %d times in the environment", k, n)
		}
	}
}

// The failure names what is missing, because the alternative is a helmfile template error that names
// a template function rather than the variable a caller forgot.
func TestPreviewEnv_MissingKeyIsNamed(t *testing.T) {
	_, err := previewEnv("ns", "app", "2", []string{"DOCKER_REGISTRY=reg"})
	if err == nil {
		t.Fatal("previewEnv accepted an environment with no DOCKER_REGISTRY_ORG and no VERSION")
	}
	for _, want := range []string{"DOCKER_REGISTRY_ORG", "VERSION"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}

// helm reads $DOCKER_CONFIG/config.json, and the pod's registry credentials are the ones Tekton
// wrote for the image build. Without them `helm pull oci://…` on the private registry answers 403.
//
// proven-by: TestWithRegistryCreds_LeavesAnExplicitConfigAlone
func TestWithRegistryCreds_LeavesAnExplicitConfigAlone(t *testing.T) {
	base := []string{"DOCKER_CONFIG=/somewhere/else"}
	got := withRegistryCreds(base)
	if len(got) != 1 || got[0] != "DOCKER_CONFIG=/somewhere/else" {
		t.Errorf("withRegistryCreds overrode a caller's DOCKER_CONFIG: %v", got)
	}
}
