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

func TestPayloadAndClaimAgree(t *testing.T) {
	tok := jwt(t, map[string]any{"scp": []any{"a"}, "aud": "x", "iss": "https://issuer"})
	p, err := payload([]byte(tok))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(p), &m); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, p)
	}
	if m["iss"] != "https://issuer" {
		t.Errorf("payload lost iss: %s", p)
	}
	// The two share a decoder precisely so they cannot disagree; assert it,
	// because "claim says one thing and payload another" is the bug that
	// having two code paths would produce.
	got, err := claim([]byte(tok), "aud")
	if err != nil || got != "x" {
		t.Errorf("claim(aud) = %q, %v", got, err)
	}
	if _, err := payload([]byte("not-a-token")); err == nil {
		t.Error("payload accepted a non-token; an unreadable token must be an error")
	}
}

// A forged token exists to show that a resource server checks signatures
// rather than merely parsing tokens. Its claims must therefore be perfect
// and its signature unverifiable — anything else and a rejection is about
// the claims instead.
func TestForgeProducesAnUnverifiableTokenWithIntactClaims(t *testing.T) {
	in := `{"sub":"forged-subject","aud":["leartech-maestro-service"],"scp":["leartechapi.internal_services"]}`
	tok, err := forge([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Split(tok, ".")); n != 3 {
		t.Fatalf("forged token has %d segments, want 3", n)
	}
	if got, _ := claim([]byte(tok), "aud"); got != "leartech-maestro-service" {
		t.Errorf("forged aud = %q; a rejection must not be attributable to the claims", got)
	}
	if got, _ := claim([]byte(tok), "scp"); got != "leartechapi.internal_services" {
		t.Errorf("forged scp = %q", got)
	}

	// Two forgeries must differ in signature: a fixed key would make this a
	// credential someone could add to a JWKS, and then the test it supports
	// would silently start passing for the wrong reason.
	again, err := forge([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	sig := func(s string) string { return strings.Split(s, ".")[2] }
	if sig(tok) == sig(again) {
		t.Error("two forgeries share a signature, so the signing key is not random")
	}
	// Same claims, so only the signature may differ.
	if strings.Join(strings.Split(tok, ".")[:2], ".") != strings.Join(strings.Split(again, ".")[:2], ".") {
		t.Error("forgeries differ in header or payload, not just signature")
	}

	if _, err := forge([]byte(`{}`)); err == nil {
		t.Error("forging with no claims should be refused: such a token would be rejected " +
			"for its empty payload rather than its signature, proving nothing")
	}
	if _, err := forge([]byte(`not json`)); err == nil {
		t.Error("forge accepted non-JSON claims")
	}
}

func TestCount(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "a bare array", in: `[{"a":1},{"a":2},{"a":3}]`, want: "3"},
		{name: "an empty array is 0, not an error", in: `[]`, want: "0"},
		{name: "an array under a key", in: `{"users":[{"id":"1"},{"id":"2"}]}`, want: "2"},
		// The distinction that matters: a request that failed must not read as
		// "there are none". A suite asserting "0 sessions remain" after logout
		// would otherwise pass against an error page.
		{name: "an HTML error page is an error, not 0", in: `<html>502</html>`, wantErr: true},
		{name: "an object with no array is an error, not 0", in: `{"error":"nope"}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := count([]byte(tc.in))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("count(%s) = %q with no error; a payload that could not be read "+
						"must not report a count", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("count(%s) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

func TestPick(t *testing.T) {
	const users = `{"users":[
		{"id":"u1","email":"a@x.com","role":"admin"},
		{"id":"u2","email":"noperm@leartech.com","role":"none"}
	]}`

	got, err := pick([]byte(users), "users", "email=noperm@leartech.com", "id")
	if err != nil || got != "u2" {
		t.Errorf(`pick(users, email=noperm@leartech.com, id) = %q, %v; want "u2"`, got, err)
	}

	// Absence is an answer, not a failure: a suite asserts "the deleted user is
	// gone" by picking and expecting empty. An error there would be
	// indistinguishable from the request failing.
	got, err = pick([]byte(users), "users", "email=deleted@x.com", "id")
	if err != nil || got != "" {
		t.Errorf("a non-matching pick returned %q, %v; want empty and no error", got, err)
	}

	// A bare array, no wrapping key.
	got, err = pick([]byte(`[{"n":"a","v":"1"},{"n":"b","v":"2"}]`), ".", "n=b", "v")
	if err != nil || got != "2" {
		t.Errorf(`pick(".", n=b, v) = %q, %v; want "2"`, got, err)
	}

	// These must ERROR rather than return empty, because empty means "no such
	// entry" and these mean "I could not look".
	for _, tc := range []struct{ name, in, key, match string }{
		{"not JSON at all", `<html>500</html>`, "users", "email=a"},
		{"no such array field", `{"people":[]}`, "users", "email=a"},
		{"the field is not an array", `{"users":"nope"}`, "users", "email=a"},
		{"match is not key=value", users, "users", "email"},
	} {
		if _, err := pick([]byte(tc.in), tc.key, tc.match, "id"); err == nil {
			t.Errorf("%s: pick returned no error; empty would then mean both 'no match' and "+
				"'could not read'", tc.name)
		}
	}
}

func TestURLDecode(t *testing.T) {
	got, err := urldecode([]byte("https%3A%2F%2Fx.example%2Fcb%3Fcode%3Dabc+123"))
	if err != nil || got != "https://x.example/cb?code=abc 123" {
		t.Errorf("urldecode = %q, %v", got, err)
	}
	if _, err := urldecode([]byte("%zz")); err == nil {
		t.Error("malformed encoding was accepted; a half-decoded value is worse than an error")
	}
}
