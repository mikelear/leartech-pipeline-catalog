// cluster.go reads what the CLUSTER already knows, so a config file does not have to repeat it.
//
// This is the port of the incumbent's two lookups, both of which `jx gitops webhook update`
// did before it did anything else:
//
//	the endpoint  services.GetServiceURLFromName(kube, "hook", ns) + "/hook"
//	the secret    Secret lighthouse-hmac-token, key "hmac"
//
// A hook belongs to the cluster that serves it. Writing the URL per repo in a file is how the
// estate ended up with it-test/lh-test pointing at hook-forge.jx and it-test/lh-private at
// hook-jx — two endpoints, no decision recorded anywhere. Reading it from the cluster makes the
// running cluster the answer, and makes a copy of this file portable to another cluster
// without editing it.
package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// ClusterSources is what the cluster supplied, and what it could not.
type ClusterSources struct {
	HookURL string   `json:"hook_url,omitempty"`
	HMAC    string   `json:"-"`
	Ingress string   `json:"ingress,omitempty"`
	Errors  []string `json:"errors,omitempty"`
}

// nsArg is the namespace every lookup uses. The dev namespace is where Lighthouse's `hook`
// ingress and the HMAC secret live, and it is `jx` on this estate.
const defaultDevNamespace = "jx"

// kubectl runs one kubectl read and returns its trimmed stdout. A missing cluster is not an
// error to raise here: the tool also runs on a laptop and in a test, and each caller decides
// whether it can proceed without what the cluster would have said.
func kubectl(args ...string) string {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hookURLFromCluster builds the Lighthouse hook endpoint from the cluster's `hook` ingress.
//
// The host comes from the ingress, not from a naming convention: the convention is
// hook-jx.<domain> here and hook-jx.az.<domain> on the other cluster, and hardcoding either
// would be wrong on the third.
// proven-by: TestHookURLFromCluster_BuildsFromTheIngressHost
func hookURLFromCluster(namespace string) (url, ingress string) {
	host := kubectl("-n", namespace, "get", "ingress", "hook", "-o", "jsonpath={.spec.rules[0].host}")
	if host == "" {
		return "", ""
	}
	return "https://" + host + "/hook", host
}

// hmacFromCluster reads the secret Lighthouse validates deliveries with, by the name and key
// the incumbent used.
//
// proven-by: TestHMACFromCluster_NamesTheSecretItCouldNotRead
func hmacFromCluster(namespace string) (string, error) {
	const secret, key = "lighthouse-hmac-token", "hmac"
	v := kubectl("-n", namespace, "get", "secret", secret, "-o", "jsonpath={.data."+key+"}")
	if v == "" {
		return "", fmt.Errorf("secret %s/%s (key %s) not found: pass -secret-env or set the hook's secret", namespace, secret, key)
	}
	// kubectl returns base64 for .data; `-o jsonpath` does not decode it.
	decoded := kubectl("-n", namespace, "get", "secret", secret, "-o", "go-template={{index .data \""+key+"\" | base64decode}}")
	if decoded == "" {
		return "", fmt.Errorf("secret %s/%s key %s is present but did not decode", namespace, secret, key)
	}
	return decoded, nil
}

// readCluster fills in what the config left out.
func readCluster(namespace string) ClusterSources {
	var cs ClusterSources
	if namespace == "" {
		return cs
	}
	cs.HookURL, cs.Ingress = hookURLFromCluster(namespace)
	if cs.HookURL == "" {
		cs.Errors = append(cs.Errors, fmt.Sprintf("no `hook` ingress in namespace %s, so the hook URL has to come from the config", namespace))
	}
	if h, err := hmacFromCluster(namespace); err == nil {
		cs.HMAC = h
	} else {
		cs.Errors = append(cs.Errors, err.Error())
	}
	return cs
}
