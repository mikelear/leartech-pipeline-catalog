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
