package main

import "testing"

// The namespace name IS the record of the preview — there is no Preview CRD in the new design, and
// clear-stuck-release already asserts this shape before it deletes anything.
//
// proven-by: TestPreviewNamespace_MatchesTheShapeGuardsTrust
func TestPreviewNamespace_MatchesTheShapeGuardsTrust(t *testing.T) {
	got := previewNamespace("mikelear", "leartech-auth-service", "118")
	if want := "jx-mikelear-leartech-auth-service-pr-118"; got != want {
		t.Errorf("previewNamespace = %q, want %q", got, want)
	}
}

// The NAMESPACE is named after the REPOSITORY and the host after the APP (the chart directory), which
// is the asymmetry jx used: jx-mikelear-leartech-forge-api-pr-17 hosts
// leartech-forge-api-pr17.jx.leartech.com. A template repo whose chart keeps its original name is
// where the two differ, and getting it backwards deploys into a namespace nobody looks in.
//
// proven-by: TestDefaultNamespace_UsesTheRepositoryNotTheApp
func TestDefaultNamespace_UsesTheRepositoryNotTheApp(t *testing.T) {
	t.Setenv("REPO_OWNER", "it-test")
	t.Setenv("REPO_NAME", "ship-proven-go-service")

	got := defaultNamespace("leartech-go-service-template", "2")
	if want := "jx-it-test-ship-proven-go-service-pr-2"; got != want {
		t.Errorf("defaultNamespace = %q, want %q", got, want)
	}
}

// A preview namespace holds the peer release's route as well as the app's, and the peer is not the
// service under test. Taking the first host reported the peer's URL for a preview that was up on its
// own — measured on the golden template, where the peer's route sorted first.
//
// proven-by: TestRouteHost_PrefersTheAppsRouteOverAPeers
func TestRouteHost_PrefersTheAppsRouteOverAPeers(t *testing.T) {
	routes := parseRoutes("preview-peer preview-peer-pr2.leartech.test\n" +
		"ship-proven-go-service ship-proven-go-service-pr2.leartech.test\n")

	if got, want := routeHost(routes, "ship-proven-go-service"), "ship-proven-go-service-pr2.leartech.test"; got != want {
		t.Errorf("routeHost = %q, want %q", got, want)
	}
}

// A control: a namespace whose only route is not named after the app still yields a host, so the
// fallback keeps a preview usable where the chart's naming differs from APP_NAME.
func TestRouteHost_FallsBackWhenNoRouteIsNamedAfterTheApp(t *testing.T) {
	routes := parseRoutes("some-ingress some-ingress-pr2.leartech.test\n")
	if got := routeHost(routes, "ship-proven-go-service"); got != "" {
		t.Errorf("routeHost = %q, want the empty string so the caller can fall back", got)
	}
}
