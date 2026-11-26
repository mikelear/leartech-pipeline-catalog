package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeJXValues writes preview/jx-values.yaml from the cluster's identity.
//
// THE FILE IS A PLACEHOLDER IN EVERY SERVICE REPO:
//
//	# Replaced by jx preview create at runtime with cluster's jxRequirements.
//	dummy: value
//
// and the preview helmfiles then read `.Values.jxRequirements.cluster.chartRepository`. Measured
// across five repos the complete set of keys any preview helmfile uses is:
//
//	cluster.chartRepository   the OCI chart repository the release lives in
//	ingress.domain            the hostname suffix the preview answers on
//
// both of which this cluster already publishes in jx-cluster-config. So writing two keys from the
// cluster is what lets an UNCHANGED preview helmfile deploy with no JX in the path.
//
// ChartRepository has no direct ConfigMap key: it is the registry plus the org's chart repository,
// because that is how the estate publishes charts (…/product-first + oci-charts).
func writeJXValues(dir, domain string) error {
	registry := os.Getenv("DOCKER_REGISTRY")
	org := os.Getenv("DOCKER_REGISTRY_ORG")
	chartRepo := os.Getenv("JX_CHART_REPOSITORY")
	if chartRepo == "" && registry != "" && org != "" {
		chartRepo = fmt.Sprintf("%s/%s-charts", registry, org)
	}
	if chartRepo == "" {
		return fmt.Errorf("no chart repository: set JX_CHART_REPOSITORY, or DOCKER_REGISTRY and DOCKER_REGISTRY_ORG")
	}
	if domain == "" {
		return fmt.Errorf("no ingress domain: set DOMAIN (the preview host is <app>-pr<n>.<DOMAIN>)")
	}

	body := fmt.Sprintf(""+
		"# Written by sp-preview create. Replaces the placeholder every service repo carries, which\n"+
		"# jx preview used to overwrite at runtime. Only these two keys are read by any preview\n"+
		"# helmfile in this estate, and both are the cluster's own facts.\n"+
		"jxRequirements:\n"+
		"  cluster:\n"+
		"    chartRepository: %q\n"+
		"  ingress:\n"+
		"    domain: %q\n", chartRepo, domain)

	path := filepath.Join(dir, "preview", "jx-values.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
