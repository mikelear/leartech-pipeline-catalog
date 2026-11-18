// identitystd checks a LIVE cluster dump for the one invariant no chart can carry.
//
// The Workload Identity annotation is applied out of band — by `kubectl annotate`
// in a bootstrap script, or by a helm value on a chart we do not own. Every gate we
// had therefore saw it as fine, and a re-render of the world silently removed it:
//
//	secret-infra/ServiceAccount external-secrets   annotation EMPTY after a re-render
//	ClusterSecretStore                              still reported Valid (its check makes
//	                                                no live call)
//	all 7 ExternalSecrets                           "could not get secret data from provider"
//
// So the assertion has to run against the cluster after sync, and this tool takes a
// `kubectl` dump rather than a kubeconfig: the dump is the artefact, it can be a
// fixture, and the invocation stays wherever the post-sync gate lives.
//
//	kubectl get sa -A -o json > sa.json
//	identitystd -sa sa.json -require external-secrets=external-secrets-system \
//	                            -require argocd-repo-server=argocd
//
// S6 (chartstd) says a chart must not SET this annotation. S9 says a ServiceAccount
// that NEEDS one must HAVE one — the mirror image, and the only place it is visible
// is the live object.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

const annotation = "iam.gke.io/gcp-service-account"

type saList struct {
	Items []struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	} `json:"items"`
}

// requirement is "<sa-name>=<namespace>", repeatable.
type requirement struct{ name, namespace string }

type reqFlag []requirement

func (r *reqFlag) String() string {
	parts := make([]string, 0, len(*r))
	for _, x := range *r {
		parts = append(parts, x.name+"="+x.namespace)
	}
	return strings.Join(parts, ",")
}

func (r *reqFlag) Set(v string) error {
	name, ns, ok := strings.Cut(v, "=")
	if !ok || name == "" || ns == "" {
		return fmt.Errorf("want <serviceaccount>=<namespace>, got %q", v)
	}
	*r = append(*r, requirement{name: name, namespace: ns})
	return nil
}

func main() {
	var dump string
	var reqs reqFlag
	flag.StringVar(&dump, "sa", "", "a `kubectl get sa -A -o json` dump")
	flag.Var(&reqs, "require",
		"a ServiceAccount that needs a Workload Identity binding, as <name>=<namespace>; "+
			"repeatable")
	flag.Parse()

	if dump == "" || len(reqs) == 0 {
		fmt.Fprintln(os.Stderr, "identitystd: -sa and at least one -require are needed. A run "+
			"with no requirements checks nothing, which is the failure mode this tool exists "+
			"for.")
		os.Exit(2)
	}

	b, err := os.ReadFile(dump) // #nosec G304 -- operator-supplied dump
	if err != nil {
		fmt.Fprintln(os.Stderr, "identitystd:", err)
		os.Exit(2)
	}
	var list saList
	if err := json.Unmarshal(b, &list); err != nil {
		fmt.Fprintln(os.Stderr, "identitystd: the dump is not `kubectl get sa -A -o json`:", err)
		os.Exit(2)
	}
	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stderr, "identitystd: the dump has no ServiceAccounts. An empty dump "+
			"and a working cluster with no bindings look identical, so this cannot pass.")
		os.Exit(2)
	}

	found := map[string]string{} // "ns/name" -> annotation value
	for _, it := range list.Items {
		found[it.Metadata.Namespace+"/"+it.Metadata.Name] = it.Metadata.Annotations[annotation]
	}

	var bad []string
	for _, r := range reqs {
		key := r.namespace + "/" + r.name
		v, ok := found[key]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("%s: ServiceAccount does not exist. The object the "+
				"annotation belongs to is gone, so nothing can use the binding", key))
		case strings.TrimSpace(v) == "":
			bad = append(bad, fmt.Sprintf("%s: has NO %s. Whatever needs Google credentials "+
				"now fails with a provider error that names neither the annotation nor this "+
				"object — an ExternalSecret reports `could not get secret data from provider` "+
				"while its ClusterSecretStore reports Valid", key, annotation))
		default:
			fmt.Printf("  ok   %s -> %s\n", key, v)
		}
	}

	if len(bad) == 0 {
		fmt.Printf("==> identitystd: %d required binding(s) present\n", len(reqs))
		return
	}
	sort.Strings(bad)
	for _, b := range bad {
		fmt.Println("S9 " + b)
	}
	fmt.Fprintf(os.Stderr, "\nFAIL: %d ServiceAccount(s) need a Workload Identity binding and "+
		"do not have one.\n", len(bad))
	os.Exit(1)
}
