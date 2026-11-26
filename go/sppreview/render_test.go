package main

import (
	"strings"
	"testing"
)

// helmfile writes its progress into the same stream as the manifests, and the consumer of a render
// is chartstd — which refuses the whole file on `cannot unmarshal !!str "Adding ..." into map`.
//
// proven-by: TestCleanRender_DropsHelmfileChatterAndKeepsTheManifests
func TestCleanRender_DropsHelmfileChatterAndKeepsTheManifests(t *testing.T) {
	in := `Adding repo jx3 https://jenkins-x-charts.github.io/repo
"jx3" has been added to your repositories
Templating release=jx-verify, chart=jx3/jx-verify
---
kind: ServiceAccount
metadata:
  name: a
---
Templating release=preview, chart=../charts/app
---
kind: Service
metadata:
  name: b
`
	got := cleanRender(in)
	if strings.Contains(got, "Adding repo") || strings.Contains(got, "Templating") {
		t.Errorf("chatter survived: %q", got)
	}
	if strings.Count(got, "kind:") != 2 {
		t.Errorf("want both manifests kept, got %q", got)
	}
	if !strings.Contains(got, "name: a") || !strings.Contains(got, "name: b") {
		t.Errorf("a manifest was dropped: %q", got)
	}
}

// A control: a clean render is unchanged in substance, so the cleaner cannot be the reason a check
// passes or fails.
func TestCleanRender_KeepsACleanRenderIntact(t *testing.T) {
	in := "---\nkind: ConfigMap\nmetadata:\n  name: x\n"
	if got := cleanRender(in); !strings.Contains(got, "name: x") || strings.Count(got, "kind:") != 1 {
		t.Errorf("cleanRender mangled a clean render: %q", got)
	}
}
