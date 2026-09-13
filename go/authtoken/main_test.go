package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// jwt builds a token whose payload is the given claims. Only the payload is
// real; the header and signature are placeholders, because nothing here
// verifies a signature and pretending otherwise would suggest it does.
func jwt(t *testing.T, claims map[string]any) string {
	t.Helper()
	b, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(b) + ".not-a-signature"
}

func TestClaim(t *testing.T) {
	for _, tc := range []struct {
		name, claim, want string
		in                string
		wantErr           bool
	}{
		{name: "array of scopes joins with spaces", claim: "scp", want: "a b",
			in: "PLACEHOLDER"},
		{name: "a string claim passes through", claim: "aud", want: "leartech-artifact-api",
			in: "PLACEHOLDER"},
		{name: "absent claim is empty, not an error", claim: "nope", want: ""},
		{name: "empty array is empty", claim: "empty", want: ""},
		// This is the case that matters most: Hydra issues scp: [] when no
		// scope is requested, and an empty result must be distinguishable from
		// a token that could not be read at all.
		{name: "not a JWT is an error, not an empty scope list", claim: "scp", want: "", wantErr: true,
			in: "this-is-not-a-token"},
		{name: "two segments only", claim: "scp", want: "", wantErr: true, in: "aaa.bbb"},
		{name: "payload not base64", claim: "scp", want: "", wantErr: true, in: "aaa.!!!!.ccc"},
		{name: "payload not JSON", claim: "scp", want: "", wantErr: true,
			in: "aaa." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".ccc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			if in == "PLACEHOLDER" || in == "" {
				in = jwt(t, map[string]any{
					"scp":   []any{"a", "b"},
					"aud":   "leartech-artifact-api",
					"empty": []any{},
				})
			}
			got, err := claim([]byte(in), tc.claim)
			if tc.wantErr && err == nil {
				t.Fatalf("claim(%q) returned %q with no error; an unreadable token must not "+
					"read as a token with no scopes", in, got)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("claim: %v", err)
			}
			if got != tc.want {
				t.Errorf("claim(%s) = %q, want %q", tc.claim, got, tc.want)
			}
		})
	}
}

// Real Hydra pads nothing, but issuers exist that do. Both must decode, or a
// scope assertion turns into an issuer-compatibility bug.
func TestClaimAcceptsPaddedPayload(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"scp": []any{"leartechapi:artifact:read"}})
	padded := base64.URLEncoding.EncodeToString(b) // includes '=' padding
	if !strings.Contains(padded, "=") {
		t.Skip("this payload happens not to need padding; the test proves nothing here")
	}
	got, err := claim([]byte("h."+padded+".s"), "scp")
	if err != nil {
		t.Fatalf("padded payload rejected: %v", err)
	}
	if got != "leartechapi:artifact:read" {
		t.Errorf("got %q", got)
	}
}

func TestField(t *testing.T) {
	const resp = `{"access_token":"abc.def.ghi","expires_in":3599,"scope":"a b"}`
	got, err := field([]byte(resp), "access_token")
	if err != nil || got != "abc.def.ghi" {
		t.Errorf("field(access_token) = %q, %v", got, err)
	}
	if got, err := field([]byte(resp), "refresh_token"); err != nil || got != "" {
		t.Errorf("absent field should be empty with no error, got %q %v", got, err)
	}
	if _, err := field([]byte(resp), "expires_in"); err == nil {
		t.Error("a numeric field asked for as a string should error, not print 3599")
	}
	// Hydra returns a 400 body on invalid_scope, and the scripts pipe that
	// through here too. It is JSON, so it parses -- and access_token is
	// absent, which is the correct answer.
	if got, err := field([]byte(`{"error":"invalid_scope"}`), "access_token"); err != nil || got != "" {
		t.Errorf("error response should yield no token, got %q %v", got, err)
	}
	if _, err := field([]byte(`<html>502</html>`), "access_token"); err == nil {
		t.Error("an HTML error page must be an error, not an empty token: a proxy 502 " +
			"would otherwise look identical to a refused grant")
	}
}
