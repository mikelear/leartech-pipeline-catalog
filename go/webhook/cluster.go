// cluster.go reads what the CLUSTER already knows, so a config file does not have to repeat it.
//
// This is the port of the incumbent's two lookups, both of which `jx gitops webhook update`
// did before it touched a repo:
//
//	the endpoint  services.GetServiceURLFromName(kube, "hook", ns) + "/hook"
//	the secret    Secret lighthouse-hmac-token, key "hmac"
//
// TWO CHANGES FROM THE INCUMBENT, both forced:
//
//	THE ROUTE IS A GATEWAY API HTTPROUTE, not an Ingress. The estate already routes with Envoy
//	Gateway — `charts/cluster-routes` declares HTTPRoutes and the `hook` Ingress is a JX-boot
//	leftover that the new cluster will not have. Reading only the Ingress would find nothing
//	there and silently fall back to the config, which is the failure this file exists to
//	remove. It reads the HTTPRoute first and the Ingress second, so it works on both.
//
//	THE ROUTE IS PER PROVIDER, because the endpoint belongs to whichever Lighthouse instance
//	receives that forge's events. This estate runs two: `jx/hook` (hook-jx.jx.leartech.com,
//	GIT_KIND=github) and `forgejo/forge-webhooks` (hook-forge.jx.leartech.com, GIT_KIND=gitea),
//	the same image configured for each forge. So a provider names its route, and the URL comes
//	from the object Envoy actually serves.
package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// ClusterSources is what the cluster supplied, and what it could not.
type ClusterSources struct {
	// HookURL is the resolved endpoint for the provider whose route was read.
	HookURL string `json:"hook_url,omitempty"`
	HMAC    string `json:"-"`
	Host    string `json:"host,omitempty"`
	// Route records which object the URL came from ("httproute jx/hook"), so a run says where
	// its endpoint was defined rather than only what it was.
	Route  string   `json:"route,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// defaultDevNamespace is where the HMAC secret and the default `hook` route live on this
// estate. Both lookups take a namespace, so another cluster can point elsewhere.
const defaultDevNamespace = "jx"

// kubectl runs one kubectl read and returns its trimmed stdout, or "" on any failure. A
// missing cluster is not an error to raise here: the tool also runs on a laptop and in a test,
// and each caller decides whether it can proceed without what the cluster would have said.
func kubectl(args ...string) string { return kubectlFunc(args...) }

// kubectlFunc is the read itself, a variable so a test exercises the two-route fallback
// without a cluster. Only a test assigns it.
var kubectlFunc = func(args ...string) string {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// routeRef is a route name with an optional namespace, "name" or "namespace/name".
type routeRef struct {
	Namespace string
	Name      string
}

// parseRoute splits a route reference, defaulting the namespace.
func parseRoute(s, defaultNS string) routeRef {
	if i := strings.Index(s, "/"); i >= 0 {
		return routeRef{Namespace: s[:i], Name: s[i+1:]}
	}
	return routeRef{Namespace: defaultNS, Name: s}
}

// String renders the route the way a message quotes it.
func (r routeRef) String() string { return r.Namespace + "/" + r.Name }

// hostFromRoute reads the first hostname of an HTTPRoute, falling back to the Ingress object
// of the same name so this works on a cluster that still has one.
//
// The host comes from the route, not from a naming convention: the convention is
// hook-jx.<domain> here, hook-forge.<domain> for the Forgejo receiver and hook-jx.az.<domain> on
// the other cluster, and hardcoding any of them would be wrong on the fourth.
// proven-by: TestHostFromRoute_PrefersHTTPRouteThenIngress
func hostFromRoute(r routeRef) (host, source string) {
	if h := kubectl("-n", r.Namespace, "get", "httproute", r.Name, "-o", "jsonpath={.spec.hostnames[0]}"); h != "" {
		return h, "httproute " + r.String()
	}
	if h := kubectl("-n", r.Namespace, "get", "ingress", r.Name, "-o", "jsonpath={.spec.rules[0].host}"); h != "" {
		return h, "ingress " + r.String()
	}
	return "", ""
}

// hookURLFromRoute builds the endpoint an HTTPRoute serves. Both hook routes listen on the
// Gateway's https section and take their path from the receiver, which is /hook.
// proven-by: TestHookURLFromRoute_BuildsFromTheHost
func hookURLFromRoute(r routeRef) ClusterSources {
	host, source := hostFromRoute(r)
	if host == "" {
		return ClusterSources{Errors: []string{fmt.Sprintf(
			"no HTTPRoute or Ingress %q in namespace %s, so the hook URL has to come from the config or -url",
			r.Name, r.Namespace)}}
	}
	return ClusterSources{HookURL: "https://" + host + "/hook", Host: host, Route: source}
}

// hmacFromCluster reads the secret Lighthouse validates deliveries with, by the name and key
// the incumbent used.
// proven-by: TestHMACFromCluster_NamesWhatItCouldNotRead
func hmacFromCluster(namespace string) (string, error) {
	const secret, key = "lighthouse-hmac-token", "hmac"
	if kubectl("-n", namespace, "get", "secret", secret, "-o", "jsonpath={.data."+key+"}") == "" {
		return "", fmt.Errorf("secret %s/%s (key %s) not found: pass -secret-env, or set the hook's secret",
			namespace, secret, key)
	}
	// .data is base64; `base64decode` is the only way to get the value out of jsonpath.
	decoded := kubectl("-n", namespace, "get", "secret", secret,
		"-o", "go-template={{index .data \""+key+"\" | base64decode}}")
	if decoded == "" {
		return "", fmt.Errorf("secret %s/%s key %s is present but did not decode", namespace, secret, key)
	}
	return decoded, nil
}

// readCluster fills in what one provider's config left out.
func readCluster(route routeRef, secretNS string) ClusterSources {
	cs := hookURLFromRoute(route)
	if secretNS != "" {
		if h, err := hmacFromCluster(secretNS); err == nil {
			cs.HMAC = h
		} else {
			cs.Errors = append(cs.Errors, err.Error())
		}
	}
	return cs
}
