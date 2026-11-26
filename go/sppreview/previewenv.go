package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// requiredPreviewEnv is what a preview helmfile demands from the environment, measured against the
// golden template's preview/helmfile.yaml.gotmpl and the two values templates it includes:
//
//	PREVIEW_NAMESPACE                        requiredEnv, names the release namespace
//	APP_NAME                                 requiredEnv, the image name and the <app>-pr<n> host
//	PULL_NUMBER                              requiredEnv, the -pr<n> host suffix
//	DOCKER_REGISTRY, DOCKER_REGISTRY_ORG     requiredEnv, the image's registry and org
//	VERSION                                  requiredEnv, the tag the pull request built
//
// `jx preview create` exported all of these, and sppreview did not: an unchanged preview helmfile then
// failed on `requiredEnv "PREVIEW_NAMESPACE"` before it rendered a single object.
//
// proven-by: TestPreviewEnv_CarriesEveryKeyTheHelmfileRequires
var requiredPreviewEnv = []string{
	"PREVIEW_NAMESPACE",
	"APP_NAME",
	"PULL_NUMBER",
	"DOCKER_REGISTRY",
	"DOCKER_REGISTRY_ORG",
	"VERSION",
}

// previewEnv is the environment a preview helmfile runs with: the caller's own, with the three values
// this binary derives taking precedence, and every key the helmfile requires checked for.
func previewEnv(namespace, appName, pullNumber string, base []string) ([]string, error) {
	derived := map[string]string{
		"PREVIEW_NAMESPACE": namespace,
		"APP_NAME":          appName,
		"PULL_NUMBER":       pullNumber,
	}

	env := make([]string, 0, len(base)+len(derived))
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); derived[k] != "" {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range derived {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}

	present := map[string]bool{}
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); v != "" {
			present[k] = true
		}
	}
	var missing []string
	for _, k := range requiredPreviewEnv {
		if !present[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the preview helmfile requires %s, and %s is not set",
			strings.Join(requiredPreviewEnv, ", "), strings.Join(missing, ", "))
	}
	return withRegistryCreds(env), nil
}

// tektonCreds is where the pod's registry credentials are written by Tekton's creds-init: the same
// file the image build copies to /kaniko/.docker/config.json. helm reads $DOCKER_CONFIG/config.json,
// and without it `helm pull oci://…` on this estate's private registry fails on a 403 fetching an
// anonymous token.
const tektonCreds = "/tekton/creds/.docker/config.json"

// withRegistryCreds points helm at those credentials, copying them where it can write. A caller that
// set DOCKER_CONFIG itself is left alone, and a pod without the file is left alone too — this only
// fills in what the cluster already provides.
// proven-by: TestWithRegistryCreds_LeavesAnExplicitConfigAlone
func withRegistryCreds(base []string) []string {
	for _, kv := range base {
		if k, v, _ := strings.Cut(kv, "="); k == "DOCKER_CONFIG" && v != "" {
			return base
		}
	}
	if _, err := os.Stat(tektonCreds); err != nil {
		return base
	}
	dir := filepath.Join(os.TempDir(), "docker-config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return base
	}
	body, err := os.ReadFile(tektonCreds)
	if err != nil {
		return base
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		return base
	}
	return append(base, "DOCKER_CONFIG="+dir)
}
