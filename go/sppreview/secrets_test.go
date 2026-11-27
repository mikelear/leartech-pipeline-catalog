package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The first real repo this ran on — GitHub, not the template — still carried the jx hook every older
// preview has, and helmfile answers an unexecutable hook with a PANIC rather than an error.
//
// proven-by: TestHelmfileJXHook_FindsTheHookThatCrashesHelmfile
func TestHelmfileJXHook_FindsTheHookThatCrashesHelmfile(t *testing.T) {
	body := `releases:
- chart: leartech/x
  hooks:
  - events: ["presync"]
    command: "jx"
    args: [secret, copy]
`
	err := helmfileJXHook("preview/helmfile.yaml.gotmpl", body)
	if err == nil {
		t.Fatal("helmfileJXHook accepted a composition whose presync hook calls jx")
	}
	if !strings.Contains(err.Error(), "secret.jenkins-x.io/replica-source=true") {
		t.Errorf("the message does not say what replaces the hook: %v", err)
	}
}

// A control, and the common case: a composition whose hooks are ordinary commands is left alone, and so
// is one with no hooks at all.
func TestHelmfileJXHook_LeavesACompositionWithoutAJXHook(t *testing.T) {
	bodies := []string{
		"releases:\n- chart: leartech/x\n",
		"releases:\n- chart: leartech/x\n  hooks:\n  - events: [\"presync\"]\n    command: \"sh\"\n    args: [-c, true]\n",
		"# command: jx is what this hook used to run\n",
	}
	for _, body := range bodies {
		if err := helmfileJXHook("preview/helmfile.yaml.gotmpl", body); err != nil {
			t.Errorf("helmfileJXHook rejected a composition it should accept: %v\n%s", err, body)
		}
	}
}

// A copy is a NEW object, not the same one moved: uid, resourceVersion and creationTimestamp belong to
// the original, and the label that marks the source must not travel or a copied Secret becomes a
// replica source in its own right.
//
// proven-by: TestCopyReplicaSecrets_RewritesTheNamespaceAndDropsTheRuntimeFields
func TestCopyReplicaSecrets_RewritesTheNamespaceAndDropsTheRuntimeFields(t *testing.T) {
	list := `{"items":[{"apiVersion":"v1","kind":"Secret","type":"kubernetes.io/basic-auth",
	  "metadata":{"name":"tekton-git","namespace":"jx","uid":"abc","resourceVersion":"42",
	    "creationTimestamp":"2026-01-01T00:00:00Z","labels":{"secret.jenkins-x.io/replica-source":"true","keep":"me"}},
	  "data":{"username":"dQ=="}}]}`

	bodies, err := rewriteSecretsForNamespace(list, "jx-owner-app-pr-7")
	if err != nil {
		t.Fatalf("rewriteSecretsForNamespace: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("want 1 Secret, got %d", len(bodies))
	}

	var got struct {
		Kind     string         `json:"kind"`
		Type     string         `json:"type"`
		Metadata map[string]any `json:"metadata"`
		Data     map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatalf("the copy is not valid JSON: %v", err)
	}
	if got.Kind != "Secret" || got.Metadata["namespace"] != "jx-owner-app-pr-7" {
		t.Errorf("kind/namespace = %v/%v", got.Kind, got.Metadata["namespace"])
	}
	for _, gone := range []string{"uid", "resourceVersion", "creationTimestamp"} {
		if _, still := got.Metadata[gone]; still {
			t.Errorf("%s survived the copy", gone)
		}
	}
	labels, _ := got.Metadata["labels"].(map[string]any)
	if _, still := labels["secret.jenkins-x.io/replica-source"]; still {
		t.Error("the replica-source label travelled with the copy, so a copy is itself a source")
	}
	if labels["keep"] != "me" {
		t.Errorf("other labels were dropped: %v", labels)
	}
	if got.Type != "kubernetes.io/basic-auth" || got.Data["username"] != "dQ==" {
		t.Errorf("type/data were not carried over: %v/%v", got.Type, got.Data)
	}
}

// A control: nothing labelled is the normal case for a namespace with no replica sources, and it must
// produce nothing to apply rather than an error.
func TestRewriteSecretsForNamespace_EmptyListYieldsNothing(t *testing.T) {
	bodies, err := rewriteSecretsForNamespace(`{"items":[]}`, "jx-owner-app-pr-7")
	if err != nil || len(bodies) != 0 {
		t.Errorf("empty list = %d bodies, err %v; want 0, nil", len(bodies), err)
	}
}
