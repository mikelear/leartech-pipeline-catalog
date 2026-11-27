package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// replicaSelector is the jx label marking a Secret a preview needs a copy of. In this estate it is the
// pod's own git credential and registry credential.
const replicaSelector = "secret.jenkins-x.io/replica-source=true"

// helmfileJXHook finds a `command: jx` in a composition, so the failure is a sentence naming the hook
// rather than a panic out of helmfile's event bus.
//
// proven-by: TestHelmfileJXHook_FindsTheHookThatCrashesHelmfile
func helmfileJXHook(path, body string) error {
	for i, line := range strings.Split(body, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(name) != "command" {
			continue
		}
		if strings.Trim(strings.TrimSpace(value), `"'`) == "jx" {
			return fmt.Errorf("%s:%d still runs a `jx` hook, and jx is not in this image: helmfile "+
				"PANICS on a hook it cannot exec. sppreview already does what `jx secret copy` did — it "+
				"copies every Secret labelled %s from the pipeline's namespace into the preview's before "+
				"deploying — so that hook can be deleted from the composition",
				path, i+1, replicaSelector)
		}
	}
	return nil
}

// pipelineNamespace is the namespace this pod runs in, which is where `jx secret copy` read from.
func pipelineNamespace() string {
	if v := os.Getenv("PREVIEW_PIPELINE_NAMESPACE"); v != "" {
		return v
	}
	body, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// copyReplicaSecrets reproduces `jx secret copy --create-namespace --selector <label> --to <ns>`.
//
// BEFORE the deploy, because that is when the hook ran: the chart mounts these Secrets, so a preview
// that starts without them crashloops on a missing secret rather than failing to deploy.
//
// proven-by: TestCopyReplicaSecrets_RewritesTheNamespaceAndDropsTheRuntimeFields
func copyReplicaSecrets(namespace string) error {
	source := pipelineNamespace()
	if source == "" || source == namespace {
		return nil
	}
	out, err := run("", "kubectl", "-n", source, "get", "secret", "-l", replicaSelector, "-o", "json")
	if err != nil {
		return fmt.Errorf("listing replica-source Secrets in %s: %w", source, err)
	}
	bodies, err := rewriteSecretsForNamespace(out, namespace)
	if err != nil || len(bodies) == 0 {
		return err
	}
	if nsYAML, err := run("", "kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml"); err == nil {
		if applied, err := runStdin(nsYAML, "kubectl", "apply", "-f", "-"); err != nil {
			return fmt.Errorf("creating %s: %v: %s", namespace, err, applied)
		}
	}
	for _, body := range bodies {
		if applied, err := runStdin(body, "kubectl", "apply", "-f", "-"); err != nil {
			return fmt.Errorf("copying a replica-source Secret into %s: %v: %s", namespace, err, applied)
		}
	}
	fmt.Printf("    copied %d replica-source Secret(s) from %s into %s\n", len(bodies), source, namespace)
	return nil
}

// rewriteSecretsForNamespace turns a `kubectl get secrets -o json` list into per-Secret manifests for
// another namespace. The runtime fields go (a copy is a new object, not the same one moved) and the
// label that authorised the copy goes with them, so a copied Secret is not itself a replica source.
//
// proven-by: TestCopyReplicaSecrets_RewritesTheNamespaceAndDropsTheRuntimeFields
func rewriteSecretsForNamespace(listJSON, namespace string) ([]string, error) {
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		return nil, fmt.Errorf("parsing the Secret list: %w", err)
	}
	var bodies []string
	for _, item := range list.Items {
		meta, _ := item["metadata"].(map[string]any)
		if meta == nil {
			continue
		}
		labels, _ := meta["labels"].(map[string]any)
		delete(labels, "secret.jenkins-x.io/replica-source")

		meta["namespace"] = namespace
		for _, drop := range []string{"uid", "resourceVersion", "creationTimestamp", "generation",
			"ownerReferences", "managedFields", "selfLink", "finalizers"} {
			delete(meta, drop)
		}
		if annotations, ok := meta["annotations"].(map[string]any); ok {
			annotations["sppreview.jenkins-x.io/copied-from"] = fmt.Sprint(meta["name"])
		}

		item["apiVersion"] = "v1"
		item["kind"] = "Secret"
		body, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("re-encoding Secret %v: %w", meta["name"], err)
		}
		bodies = append(bodies, string(body))
	}
	return bodies, nil
}
