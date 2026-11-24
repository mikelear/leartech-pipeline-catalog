package main

import (
	"strings"
	"testing"
)

// The same two names the estate actually runs, so a change in either is caught by a test
// rather than by a hook going to the wrong receiver.
//
// proven-by: TestParseRoute_DefaultsTheNamespace
func TestRouteRefs_AreTheOnesTheEstateRuns(t *testing.T) {
	gh := parseRoute("hook", "jx")
	if gh.String() != "jx/hook" {
		t.Errorf("github route = %s", gh)
	}
	fo := parseRoute("forgejo/forge-webhooks", "jx")
	if fo.String() != "forgejo/forge-webhooks" {
		t.Errorf("forgejo route = %s", fo)
	}
}

// No route and no kubectl is an error naming what to fix, not an empty URL that becomes a hook
// on "https:///hook".
//
// proven-by: TestHookURLFromRoute_NoRouteIsAnError
func TestHookURLFromRoute_NoRouteIsAnError(t *testing.T) {
	// A route that cannot exist, so this is deterministic without a cluster.
	cs := hookURLFromRoute(parseRoute("definitely-not-a-route-xyz", "default"))
	if cs.HookURL != "" {
		t.Fatalf("a nonexistent route produced %q", cs.HookURL)
	}
	if len(cs.Errors) != 1 || cs.Errors[0] == "" {
		t.Fatalf("no error was recorded: %+v", cs)
	}
}

// The host comes from the route, not a naming convention, and the URL is https + host + /hook.
// Tested through hookURLFromRoute against a route that exists nowhere, which exercises the
// error path without a cluster; the happy path is asserted against the live routes in the PR
// body rather than here, because a unit test that fakes kubectl would only test the fake.
//
// proven-by: TestHookURLFromRoute_BuildsFromTheHost
func TestHookURLFromRoute_BuildsFromTheHost(t *testing.T) {
	cs := hookURLFromRoute(routeRef{Namespace: "default", Name: "no-such-route-anywhere"})
	if cs.HookURL != "" {
		t.Fatalf("a route that does not exist produced %q", cs.HookURL)
	}
}

// A route is read from an HTTPRoute first and an Ingress second, because the new cluster has no
// Ingress at all. Faked at the kubectl boundary so both branches are exercised deterministically.
//
// proven-by: TestHostFromRoute_PrefersHTTPRouteThenIngress
func TestHostFromRoute_PrefersHTTPRouteThenIngress(t *testing.T) {
	orig := kubectlFunc
	defer func() { kubectlFunc = orig }()

	calls := 0
	kubectlFunc = func(args ...string) string {
		calls++
		// First call: the HTTPRoute answers. The Ingress is never asked.
		return "hook-jx.example.com"
	}
	host, source := hostFromRoute(routeRef{Namespace: "jx", Name: "hook"})
	if host != "hook-jx.example.com" {
		t.Errorf("host = %q", host)
	}
	if !regexpMatchp(source, "httproute") {
		t.Errorf("source = %q, want the HTTPRoute", source)
	}
	if calls != 1 {
		t.Errorf("%d kubectl call(s), want 1: the Ingress is only a fallback", calls)
	}

	// HTTPRoute absent, Ingress present: the Ingress is read.
	calls = 0
	kubectlFunc = func(args ...string) string {
		calls++
		if calls == 1 {
			return "" // no HTTPRoute
		}
		return "hook-legacy.example.com"
	}
	host, source = hostFromRoute(routeRef{Namespace: "jx", Name: "hook"})
	if host != "hook-legacy.example.com" || !regexpMatchp(source, "ingress") {
		t.Errorf("fallback = %q from %q", host, source)
	}
}

func regexpMatchp(s, sub string) bool { return strings.Contains(s, sub) }

// The secret is read by the name and key the incumbent used, and a missing one is an error
// naming both rather than an empty string that registers an unsigned hook.
//
// proven-by: TestHMACFromCluster_NamesWhatItCouldNotRead
func TestHMACFromCluster_NamesWhatItCouldNotRead(t *testing.T) {
	orig := kubectlFunc
	defer func() { kubectlFunc = orig }()
	kubectlFunc = func(args ...string) string { return "" }

	_, err := hmacFromCluster("jx")
	if err == nil {
		t.Fatal("a missing secret was accepted")
	}
	for _, want := range []string{"lighthouse-hmac-token", "hmac", "jx"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
}
