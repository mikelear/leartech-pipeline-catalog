// Command sppreview deploys, resolves and prunes a per-pull-request preview.
//
// The replacement for `jx preview create` / `jx preview gc`, and the reason is measurable: jx deploys
// the preview correctly and then fails to FIND it, because it asks `serving.knative.dev` for a Service
// URL and this estate routes with Envoy Gateway (there is no IngressClass at all):
//
//	failed to find preview url in preview-<app> service
//	  the server could not find the requested resource (get services.serving.knative.dev)
//	could not detect a preview URL          <- the step fails here, after a good deploy
//
// while underneath it:
//
//	jx-mikelear-leartech-forge-api-pr-17   Active
//	  preview-leartech-forge-api  1/1
//	  httproute leartech-forge-api  ["leartech-forge-api-pr17.jx.leartech.com"]
//
// So `create` does the same deploy and then reads the URL from the object Envoy actually serves.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	sub := "create"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}

	fs := flag.NewFlagSet("sppreview", flag.ExitOnError)
	dir := fs.String("dir", ".", "the service checkout")
	namespace := fs.String("namespace", "", "the preview namespace (default jx-<owner>-<app>-pr-<n> from the env)")
	pullNumber := fs.String("pull-number", os.Getenv("PULL_NUMBER"), "the pull request number")
	appName := fs.String("app-name", firstEnv("APP_NAME", "REPO_NAME"), "the app name")
	domain := fs.String("domain", os.Getenv("DOMAIN"), "the cluster's ingress domain")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the preview to become available")
	helmfileBin := fs.String("helmfile", "helmfile", "the helmfile binary")
	rendered := fs.String("render", "", "render the preview helmfile to this file and stop (no cluster, no deploy)")
	_ = fs.Parse(args)

	switch sub {
	case "create":
		if err := create(*dir, *namespace, *pullNumber, *appName, *domain, *timeout, *helmfileBin, *rendered); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(1)
		}
	case "url":
		url, err := resolveURL(*namespace, *appName, *pullNumber, *domain)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(url)
	default:
		fmt.Fprintf(os.Stderr, "sppreview: unknown command %q (want create, url)\n", sub)
		os.Exit(2)
	}
}

// create deploys the preview and waits for it to be AVAILABLE.
//
// The wait is the point. jx's "did it come up" question was answered by a Knative Service appearing, so
// on this cluster it was answered "no" for a preview that was up — and the failure named the URL
// rather than the deploy.
func create(dir, namespace, pullNumber, appName, domain string, timeout time.Duration, helmfileBin, rendered string) error {
	if namespace == "" {
		namespace = defaultNamespace(appName, pullNumber)
	}
	helmfilePath := filepath.Join(dir, "preview", "helmfile.yaml.gotmpl")
	if _, err := os.Stat(helmfilePath); err != nil {
		return fmt.Errorf("no preview/helmfile.yaml.gotmpl in %s — this repo has no preview to create", dir)
	}

	// The preview helmfiles read .Values.jxRequirements, and the file that supplies it is a
	// placeholder the JX preview step overwrote at runtime:
	//
	//	# Replaced by jx preview create at runtime with cluster's jxRequirements.
	//	dummy: value
	//
	// Measured across five service repos, the COMPLETE set of keys they read is
	// cluster.chartRepository and ingress.domain, both of which this cluster already publishes. So
	// writing the file from the cluster's identity is what lets every existing preview helmfile run
	// with no JX in the path.
	if err := writeJXValues(dir, domain); err != nil {
		return err
	}

	env, err := previewEnv(namespace, appName, pullNumber, os.Environ())
	if err != nil {
		return err
	}

	sync := []string{"--file", helmfilePath, "sync", "--namespace", namespace}
	if rendered != "" {
		// RENDER-ONLY: the same helmfile composition, into a file, with no cluster and no deploy.
		// This is what makes the preview checkable on a pull request — see the task that pipes it
		// into chartstd.
		out, err := runEnv(dir, env, helmfileBin, "--file", helmfilePath, "template", "--namespace", namespace)
		if err != nil {
			return err
		}
		// Clean YAML only: the render's consumer is the chart-standard gate, and helmfile's
		// progress lines in the same stream make it refuse the file outright.
		return os.WriteFile(rendered, []byte(cleanRender(out)), 0o644)
	}
	body, err := os.ReadFile(helmfilePath)
	if err != nil {
		return err
	}
	if err := helmfileJXHook(helmfilePath, string(body)); err != nil {
		return err
	}
	if err := copyReplicaSecrets(namespace); err != nil {
		return err
	}
	if out, err := runEnv(dir, env, helmfileBin, sync...); err != nil {
		return err
	} else if strings.TrimSpace(out) != "" {
		fmt.Println(out)
	}

	// AVAILABLE, not "the object exists". A Deployment that is still rolling has an object.
	deadline := time.Now().Add(timeout)
	for {
		out, err := run("", "kubectl", "-n", namespace, "get", "deploy",
			"-o", `jsonpath={range .items[*]}{.metadata.name}={.status.availableReplicas}{"\n"}{end}`)
		if err != nil {
			return err
		}
		pending := 0
		total := 0
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line == "" {
				continue
			}
			total++
			name, replicas, _ := strings.Cut(line, "=")
			if replicas == "" || replicas == "0" {
				fmt.Printf("    waiting: %s has no available replica\n", name)
				pending++
			}
		}
		if total > 0 && pending == 0 {
			break
		}
		if total == 0 {
			fmt.Println("    waiting: no Deployment in the namespace yet")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("preview %s did not become available within %s (%d of %d deployments)",
				namespace, timeout, pending, total)
		}
		time.Sleep(5 * time.Second)
	}

	url, err := resolveURL(namespace, appName, pullNumber, domain)
	if err != nil {
		// A preview that is UP and unroutable is a real finding, and it is the one thing jx could
		// never report: it fails on the URL for a preview it deployed.
		return fmt.Errorf("preview %s is available but has no URL: %w", namespace, err)
	}
	fmt.Printf("==> preview %s is available at %s\n", namespace, url)
	return nil
}

// resolveURL asks the object Envoy actually serves, and falls back to the estate's own convention.
//
// Both are checked rather than assumed, because they disagreeing is itself the finding: the chart
// renders an HTTPRoute, the estate's convention is <app>-pr<n>.<domain>, and a preview whose chart
// renders an ingress (there is no IngressClass here) answers neither.
func resolveURL(namespace, appName, pullNumber, domain string) (string, error) {
	if namespace == "" {
		return "", fmt.Errorf("no namespace: set -namespace, or REPO_OWNER/APP_NAME/PULL_NUMBER")
	}
	out, _ := run("", "kubectl", "-n", namespace, "get", "httproute",
		"-o", `jsonpath={range .items[*]}{.metadata.name} {.spec.hostnames[0]}{"\n"}{end}`)
	routes := parseRoutes(out)

	// THIS APP'S route, not merely the first one. A preview namespace holds more than the service: the
	// peer release renders its own route, and taking whichever sorted first reported the PEER's host
	// for a preview that was up and answering on its own host.
	if host := routeHost(routes, appName); host != "" {
		return "https://" + host, nil
	}
	for _, r := range routes {
		if r.host != "" && r.host != "<nil>" {
			return "https://" + r.host, nil
		}
	}
	if domain == "" || appName == "" || pullNumber == "" {
		return "", fmt.Errorf("no HTTPRoute in %s, and no convention to fall back on (domain/app/number)", namespace)
	}
	return fmt.Sprintf("https://%s-pr%s.%s", appName, pullNumber, domain), nil
}

// route is one HTTPRoute's name and primary hostname.
type route struct{ name, host string }

func parseRoutes(out string) []route {
	var routes []route
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, host, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		routes = append(routes, route{name: name, host: strings.TrimSpace(host)})
	}
	return routes
}

// routeHost picks the route that belongs to the app: named after it, or hosting a name that starts
// with it. The peer's route satisfies neither.
//
// proven-by: TestRouteHost_PrefersTheAppsRouteOverAPeers
func routeHost(routes []route, appName string) string {
	if appName == "" {
		return ""
	}
	for _, r := range routes {
		if r.name == appName || strings.HasPrefix(r.host, appName+"-") {
			return r.host
		}
	}
	return ""
}

// defaultNamespace names a preview after the REPOSITORY, which is what jx did and what the shape
// guard in clear-stuck-release assumes. The HOST is named after the app (the chart directory), and the
// two differ whenever a repo's chart does not share its name.
func defaultNamespace(appName, pullNumber string) string {
	return previewNamespace(os.Getenv("REPO_OWNER"), firstEnv("REPO_NAME", appName), pullNumber)
}

// previewNamespace is the estate's naming, which other steps already depend on:
// clear-stuck-release asserts the SHAPE `jx-*-pr-[0-9]*` before it deletes anything.
func previewNamespace(owner, appName, pullNumber string) string {
	return fmt.Sprintf("jx-%s-%s-pr-%s", owner, appName, pullNumber)
}

func run(dir, name string, args ...string) (string, error) {
	return runEnv(dir, nil, name, args...)
}

// runStdin is run for a child that reads its manifest from a pipe (kubectl apply -f -).
func runStdin(stdin, name string, args ...string) (string, error) {
	out, err := runStdinEnv(stdin, nil, name, args...)
	return out, err
}

func runStdinEnv(stdin string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// runEnv is run with an explicit environment, for the one child that needs one: helmfile, whose
// template is a set of requiredEnv calls.
func runEnv(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err,
			strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

var _ = context.Background
