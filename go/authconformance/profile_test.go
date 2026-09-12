package main

// The profile and its cross-check.
//
// The design under test: a repo DECLARES what kind of auth participant it is,
// and the tool CROSS-CHECKS that against what the code proves. Neither half
// works alone —
//
//   derivation alone guesses. A derived heuristic on 2026-09-12 ("a UUID
//   client_id means dynamically registered") classified the platform's own
//   oauth-frontend SPA client as a stranger holding internal audiences. A false
//   finding is worse than none: it teaches people to ignore the check.
//
//   declaration alone drifts. A service declares inbound-only, someone adds an
//   outbound call, and the declaration becomes a comment that lies.
//
// So the load-bearing tests here are the CONTRADICTION pairs, and the
// "reported, not failed" cases that keep the tool from guessing.

import (
	"strings"
	"testing"
)

const (
	inboundMain = `package main

import "github.com/mikelear/leartech-go-common/pkg/auth"

func main() { _, _ = auth.NewVerifier(nil, auth.VerifierConfig{}) }
`
	mintingMain = `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`
	dualMain = `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	_, _ = auth.NewVerifier(nil, auth.VerifierConfig{})
	c, _ := auth.NewServiceClient(nil, auth.Config{})
	_, _ = c.GetAuthToken(context.Background())
}
`
	publicMain = `package main

import "github.com/mikelear/leartech-go-common/pkg/auth"

func main() {
	_, _ = auth.NewVerifier(nil, auth.VerifierConfig{})
	http.HandleFunc("/.well-known/oauth-protected-resource", nil)
}
`
)

// ── the declaration must exist ──────────────────────────────────────────────

func TestMissingProfileIsRefused(t *testing.T) {
	rules := run(t, fixture{noAuthProfile: true}.build(t))
	if !has(rules, "profile-declared") {
		t.Fatalf("a repo with no .authprofile passed.\n\n"+
			"The correct env set, chart shape and test properties are all a function of "+
			"the service TYPE; there is no single standard set. An inbound-only service "+
			"given CLIENT_ID/CLIENT_SECRET looks like it holds an identity it never "+
			"spends, which is how inert credentials reached several charts.\n\ngot: %v", rules)
	}
}

func TestUnknownTypeIsRefused(t *testing.T) {
	rules := run(t, fixture{authProfile: "type: sort-of-a-gateway\n"}.build(t))
	if !has(rules, "profile-declared") {
		t.Fatalf("an unrecognised type was accepted: %v", rules)
	}
}

func TestProfileWithNoTypeIsRefused(t *testing.T) {
	rules := run(t, fixture{authProfile: "# nothing here\nreason: because\n"}.build(t))
	if !has(rules, "profile-declared") {
		t.Fatalf("a .authprofile with no type: was accepted: %v", rules)
	}
}

// The control for all three: a correct declaration passes.
func TestCorrectlyDeclaredInboundServicePasses(t *testing.T) {
	rules := run(t, fixture{authProfile: "type: inbound-resource-server\n"}.build(t))
	if has(rules, "profile-declared") || has(rules, "profile-matches-code") {
		t.Fatalf("a correctly declared inbound resource server was flagged: %v", rules)
	}
}

// ── contradictions ──────────────────────────────────────────────────────────

// THE DRIFT CASE. Declares inbound-only; the code mints. This is the shape that
// puts inert credentials into charts and the reason declaration alone is not
// enough.
func TestInboundDeclarationContradictedByAnOutboundLeg(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: inbound-resource-server\n",
		files:       map[string]string{"main.go": dualMain},
	}.build(t))
	if !has(rules, "profile-matches-code") {
		t.Fatalf("a service declaring inbound-only while spending an outbound token was "+
			"not flagged: %v", rules)
	}
}

func TestOutboundDeclarationContradictedByAnInboundVerifier(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: outbound-only\n",
		files:       map[string]string{"main.go": inboundMain},
	}.build(t))
	if !has(rules, "profile-matches-code") {
		t.Fatalf("a service declaring outbound-only while validating inbound tokens was "+
			"not flagged — it does expose an authenticated API and needs its own "+
			"audience: %v", rules)
	}
}

func TestDualRoleDeclarationContradictedByNoOutboundLeg(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: dual-role\n",
		files:       map[string]string{"main.go": inboundMain},
	}.build(t))
	if !has(rules, "profile-matches-code") {
		t.Fatalf("a service declaring dual-role with no outbound token spend was not "+
			"flagged. ServiceClient's validateConfig demands ClientID and ClientSecret "+
			"it would never use; their absence took plan-api off the air on "+
			"2026-08-13: %v", rules)
	}
}

func TestNoneDeclarationContradictedByAuthUsage(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: none\n",
		files:       map[string]string{"main.go": inboundMain},
	}.build(t))
	if !has(rules, "profile-matches-code") {
		t.Fatalf("a service declaring it does not participate in auth, while "+
			"constructing a verifier, was not flagged: %v", rules)
	}
}

// public-resource-server means EXTERNAL, dynamically-registered clients, which
// discover the issuer and resource identifier through RFC 9728 metadata. Absent
// that endpoint the declaration is aspirational.
func TestPublicDeclarationContradictedByNoProtectedResourceMetadata(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: public-resource-server\n",
		files:       map[string]string{"main.go": inboundMain},
	}.build(t))
	if !has(rules, "profile-matches-code") {
		t.Fatalf("a service declaring public-resource-server without publishing "+
			"/.well-known/oauth-protected-resource was not flagged: %v", rules)
	}
}

func TestPublicDeclarationAcceptedWithProtectedResourceMetadata(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: public-resource-server\n",
		files:       map[string]string{"main.go": publicMain},
	}.build(t))
	if has(rules, "profile-matches-code") {
		t.Fatalf("a public resource server that DOES publish RFC 9728 metadata was "+
			"flagged: %v", rules)
	}
}

// ── the issuer is the one type nothing can contradict ───────────────────────

// An issuer validates the tokens it mints, so every derived signal is
// ambiguous for it. That makes it the one declaration taken on trust — and
// trust needs an argument attached.
func TestIssuerTypeRequiresAReason(t *testing.T) {
	rules := run(t, fixture{authProfile: "type: issuer\n"}.build(t))
	if !has(rules, "profile-declared") {
		t.Fatalf("type: issuer was accepted with no reason. It is exempt from the "+
			"cross-check, so an undeclared reason makes it a silent bypass of every "+
			"profile rule: %v", rules)
	}
}

func TestIssuerTypeAcceptedWithAReason(t *testing.T) {
	rules := run(t, fixture{
		authProfile: "type: issuer\nreason: this service IS the Hydra-backed issuer; it\n" +
			"  validates tokens it also mints, so no derived signal can confirm it.\n",
		files: map[string]string{"main.go": dualMain},
	}.build(t))
	if has(rules, "profile-matches-code") {
		t.Fatalf("the issuer was cross-checked despite being exempt by construction: %v", rules)
	}
}

// ── it must not guess ───────────────────────────────────────────────────────

// A repo where no constructor is visible — behind an interface, a helper, or
// generated code — must be REPORTED, not failed. "I could not find a verifier"
// is not proof there is none, and a tool that fails on absence of evidence
// produces exactly the false findings that get gates switched off.
func TestUnprovableInboundDeclarationIsNotFailed(t *testing.T) {
	noConstructor := `package main

import _ "github.com/mikelear/leartech-go-common/pkg/auth"

// The verifier is built in an internal helper this fixture does not include.
func main() {}
`
	rules := run(t, fixture{
		authProfile: "type: inbound-resource-server\n",
		files:       map[string]string{"main.go": noConstructor},
	}.build(t))
	if has(rules, "profile-matches-code") {
		t.Fatalf("an inbound declaration was FAILED because no verifier call was "+
			"visible. Absence of evidence is not evidence of absence; the constructor "+
			"may be behind a helper or an interface: %v", rules)
	}
}

// ── the derivation itself ───────────────────────────────────────────────────

func TestDeriveReadsTheEvidence(t *testing.T) {
	cases := []struct {
		name string
		ev   evidence
		want serviceType
	}{
		{"verifier only", evidence{verifierAt: []string{"a.go:1"}}, typeInbound},
		{"verifier + outbound", evidence{verifierAt: []string{"a.go:1"}, outboundAt: []string{"b.go:2"}}, typeDualRole},
		{"outbound only", evidence{outboundAt: []string{"b.go:2"}}, typeOutbound},
		{"service client, no spend", evidence{serviceClientAt: []string{"c.go:3"}}, typeOutbound},
		{"nothing at all", evidence{}, typeNone},
		{"verifier + RFC 9728", evidence{verifierAt: []string{"a.go:1"}, protectedResAt: []string{"r.go"}}, typePublic},
	}
	for _, c := range cases {
		if got := c.ev.derive(); got != c.want {
			t.Errorf("%s: derived %q, want %q", c.name, got, c.want)
		}
	}
}

// The suggestion in the missing-profile message has to be right, or adopting
// the file becomes a research task and people paste the wrong type.
func TestMissingProfileMessageSuggestsTheDerivedType(t *testing.T) {
	var r report
	ev := evidence{verifierAt: []string{"main.go:5"}, outboundAt: []string{"main.go:9"}}
	loadProfile(t.TempDir(), &r, ev)

	if len(r.findings) == 0 {
		t.Fatal("no finding for a missing .authprofile")
	}
	msg := r.findings[0].msg
	if !strings.Contains(msg, string(typeDualRole)) {
		t.Errorf("the message does not suggest the derived type %q, so adopting the "+
			"file is guesswork:\n%s", typeDualRole, msg)
	}
	if !strings.Contains(msg, "main.go:5") {
		t.Errorf("the message does not cite the evidence it reasoned from, so the "+
			"reader cannot disagree with it:\n%s", msg)
	}
}

// ── the inbound half has two shapes ─────────────────────────────────────────

// THE REGRESSION. A dual-role service can validate inbound through its
// ServiceClient rather than a Verifier — maestro does `au.Middleware(nil)` and
// never calls NewVerifier. Looking only for NewVerifier derived maestro as
// "outbound-only", which is wrong in the direction that matters: it would have
// told a service that validates tokens that it does not.
func TestServiceClientMiddlewareCountsAsInbound(t *testing.T) {
	maestroShape := `package main

import (
	"context"

	"github.com/mikelear/leartech-go-common/pkg/auth"
)

func main() {
	au, _ := auth.NewServiceClient(nil, auth.Config{})
	_ = au.Middleware(nil)
	_, _ = au.GetAuthToken(context.Background())
}
`
	rules := run(t, fixture{
		authProfile: "type: dual-role\n",
		files:       map[string]string{"main.go": maestroShape},
	}.build(t))
	if has(rules, "profile-matches-code") {
		t.Fatalf("a service validating inbound via ServiceClient.Middleware was treated "+
			"as having no inbound half: %v", rules)
	}

	// And the derivation itself must say dual-role, not outbound-only.
	ev := evidence{
		verifierAt:      []string{"main.go:12"},
		serviceClientAt: []string{"main.go:11"},
		outboundAt:      []string{"main.go:13"},
	}
	if got := ev.derive(); got != typeDualRole {
		t.Errorf("derived %q, want %q", got, typeDualRole)
	}
}

// The guard on that widening. "Middleware" is an extremely common method name;
// an unguarded match would call any gin middleware an inbound auth gate and
// then fail a genuinely outbound-only service for validating tokens it does
// not. So it only counts where go-common/pkg/auth is in scope.
func TestUnrelatedMiddlewareDoesNotCountAsInbound(t *testing.T) {
	files := map[string]string{
		// Mints tokens. No inbound validation anywhere.
		"main.go": mintingMain,
		// A different file with its own Middleware and NO auth import.
		"internal/logging/mw.go": `package logging

type Chain struct{}

func (c Chain) Middleware(next func()) func() { return next }

func Use(c Chain) { _ = c.Middleware(nil) }
`,
	}
	rules := run(t, fixture{authProfile: "type: outbound-only\n", files: files}.build(t))
	if has(rules, "profile-matches-code") {
		t.Fatalf("a logging middleware in a file that does not import go-common/pkg/auth "+
			"was counted as inbound auth, contradicting a correct outbound-only "+
			"declaration: %v", rules)
	}
}
